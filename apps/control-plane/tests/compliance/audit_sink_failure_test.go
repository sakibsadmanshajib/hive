package compliance_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auditworker"
)

// TestAuditSink_FailureDoesNotBlockChat enqueues a chat-request audit
// row, attaches an outbox sink whose every Send returns an error, and
// confirms the row lands in audit_outbox_dlq after the retry budget is
// exhausted — i.e. an unhealthy sink never blocks the hot path.
func TestAuditSink_FailureDoesNotBlockChat(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	w := audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "ci", Env: "ci"})
	require.NoError(t, w.Write(ctx, audit.Event{
		Action:   "CHAT_REQUEST",
		Severity: audit.SeverityInfo,
		Actor:    audit.Actor{Type: audit.ActorUser},
	}))

	_, err := pool.Exec(ctx, `
		INSERT INTO public.audit_outbox(audit_id, audit_ts, sink)
		SELECT id, ts, 'fail-sink'
		  FROM public.audit_log
		 WHERE action = 'CHAT_REQUEST'
		 ORDER BY ts DESC
		 LIMIT 1`)
	require.NoError(t, err)

	failSink := &funcSink{
		name: "fail-sink",
		fn: func(ctx context.Context, row map[string]any) error {
			return errors.New("simulated outage")
		},
	}

	worker := auditworker.New(auditworker.Config{
		Pool:         pool,
		Sinks:        []auditworker.Sink{failSink},
		MaxAttempts:  2,
		BackoffStart: 25 * time.Millisecond,
		BackoffMax:   100 * time.Millisecond,
		PollInterval: 25 * time.Millisecond,
	})

	go worker.Run(ctx)

	require.Eventually(t, func() bool {
		var n int
		_ = pool.QueryRow(ctx,
			`SELECT count(*)::int FROM public.audit_outbox_dlq WHERE sink = 'fail-sink'`,
		).Scan(&n)
		return n > 0
	}, 10*time.Second, 200*time.Millisecond, "DLQ row must appear after retry budget")
}

type funcSink struct {
	name string
	fn   func(ctx context.Context, row map[string]any) error
}

func (f *funcSink) Name() string                                     { return f.name }
func (f *funcSink) Send(ctx context.Context, r map[string]any) error { return f.fn(ctx, r) }
