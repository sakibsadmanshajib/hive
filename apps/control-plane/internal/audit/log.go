package audit

import (
	"context"
	"errors"
)

// WALWriter mirrors SyncWriter but persists to local disk first. See wal.go.
type WALWriter interface {
	Write(ctx context.Context, e Event) error
}

type LoggerDeps struct {
	Sync *SyncWriter
	WAL  WALWriter
}

type Logger struct {
	deps LoggerDeps
}

func NewLogger(deps LoggerDeps) *Logger {
	if deps.Sync == nil {
		panic("audit.NewLogger: Sync writer required")
	}
	if deps.WAL == nil {
		panic("audit.NewLogger: WAL writer required")
	}
	return &Logger{deps: deps}
}

func (l *Logger) Deps() LoggerDeps { return l.deps }

// Log dispatches to Sync (security tier) or WAL (LLM tier) by action.
// Validates both Action and Severity at the entry so malformed events
// cannot be queued onto the WAL and stall ordered drain by repeatedly
// failing the writer's own validation.
func (l *Logger) Log(ctx context.Context, e Event) error {
	if e.Action == "" {
		return errors.New("audit: action required")
	}
	if e.Severity == "" {
		return errors.New("audit: severity required")
	}
	if IsSecurityAction(e.Action) || e.Severity == SeverityError || e.Severity == SeverityCritical {
		return l.deps.Sync.Write(ctx, e)
	}
	return l.deps.WAL.Write(ctx, e)
}
