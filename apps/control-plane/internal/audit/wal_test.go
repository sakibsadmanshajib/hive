package audit_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/hivegpt/hive/apps/control-plane/internal/audit"
)

func TestWAL_WriteThenDrain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dir := t.TempDir()
	pool := newWALPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	sync := audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "test", Env: "test"})
	wal, err := audit.NewWALWriter(audit.WALConfig{Dir: dir, Sync: sync})
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		require.NoError(t, wal.Write(ctx, audit.Event{
			TenantID:  uuid.New(),
			Actor:     audit.Actor{ID: uuid.New(), Type: audit.ActorUser},
			Action:    "CHAT_REQUEST",
			Severity:  audit.SeverityInfo,
			RequestID: uuid.New(),
		}))
	}

	entries, err := os.ReadDir(filepath.Join(dir, "events"))
	require.NoError(t, err)
	// With a real DB available, the 250ms attempt may have succeeded, so
	// the WAL file count is either 0 (all flushed) or up to 5 (all buffered).
	// Either is acceptable; the next Drain MUST leave the dir empty.
	_ = entries

	drained, err := wal.Drain(ctx)
	require.NoError(t, err)
	_ = drained // count depends on path taken above

	entries, err = os.ReadDir(filepath.Join(dir, "events"))
	require.NoError(t, err)
	require.Empty(t, entries, "WAL events dir must be empty after Drain")
}

func newWALPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool
}
