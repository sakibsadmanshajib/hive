package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

type WALConfig struct {
	Dir  string      // absolute path; e.g. /var/lib/hive/audit-wal
	Sync *SyncWriter // used by Drain() to flush stored events to Postgres
}

type FileWALWriter struct {
	cfg WALConfig
	mu  sync.Mutex
}

// Ensure WALWriter interface is satisfied.
var _ WALWriter = (*FileWALWriter)(nil)

func NewWALWriter(cfg WALConfig) (*FileWALWriter, error) {
	if cfg.Dir == "" {
		return nil, errors.New("audit: WALConfig.Dir required")
	}
	if cfg.Sync == nil {
		return nil, errors.New("audit: WALConfig.Sync required")
	}
	if err := os.MkdirAll(filepath.Join(cfg.Dir, "events"), 0o750); err != nil {
		return nil, fmt.Errorf("audit: mkdir wal: %w", err)
	}
	return &FileWALWriter{cfg: cfg}, nil
}

type walEnvelope struct {
	WrittenAt time.Time `json:"written_at"`
	Event     Event     `json:"event"`
}

// Write attempts a synchronous Postgres insert via the embedded SyncWriter.
// On Postgres failure (or 250 ms deadline), it appends a JSON envelope to
// disk and returns nil so the calling request is unaffected.
func (w *FileWALWriter) Write(ctx context.Context, e Event) error {
	deadline, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	if err := w.cfg.Sync.Write(deadline, e); err == nil {
		return nil
	}

	env := walEnvelope{WrittenAt: time.Now().UTC(), Event: e}
	raw, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("audit: wal marshal: %w", err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	name := fmt.Sprintf("%d-%s.json", time.Now().UnixNano(), uuid.NewString())
	path := filepath.Join(w.cfg.Dir, "events", name)
	if err := os.WriteFile(path, raw, 0o640); err != nil {
		return fmt.Errorf("audit: wal write: %w", err)
	}
	return nil
}

// Drain attempts to flush every WAL file in order. Returns the count
// successfully drained. A file that fails to write to Postgres remains on
// disk and the drainer stops at the first failure to preserve order.
func (w *FileWALWriter) Drain(ctx context.Context) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	dir := filepath.Join(w.cfg.Dir, "events")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("audit: wal readdir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, en := range entries {
		if en.IsDir() {
			continue
		}
		names = append(names, en.Name())
	}
	sort.Strings(names)

	drained := 0
	for _, name := range names {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return drained, fmt.Errorf("audit: wal read %s: %w", name, err)
		}
		var env walEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return drained, fmt.Errorf("audit: wal unmarshal %s: %w", name, err)
		}
		if err := w.cfg.Sync.Write(ctx, env.Event); err != nil {
			return drained, fmt.Errorf("audit: wal flush %s: %w", name, err)
		}
		if err := os.Remove(path); err != nil {
			return drained, fmt.Errorf("audit: wal remove %s: %w", name, err)
		}
		drained++
	}
	return drained, nil
}
