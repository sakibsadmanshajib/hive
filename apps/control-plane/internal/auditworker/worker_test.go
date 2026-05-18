package auditworker_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auditworker"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestWorkerDrainsOutboxToSink(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool := newWorkerPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	writer := audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"})
	require.NoError(t, writer.Write(ctx, audit.Event{
		Action:   "AUTH_SIGNIN_SUCCESS",
		Severity: audit.SeverityInfo,
		Actor:    audit.Actor{Type: audit.ActorUser},
	}))

	_, err := pool.Exec(ctx,
		`INSERT INTO public.audit_outbox(audit_id, audit_ts, sink)
		   SELECT id, ts, 'fake-sink'
		     FROM public.audit_log
		    WHERE action='AUTH_SIGNIN_SUCCESS'
		    ORDER BY ts DESC
		    LIMIT 1`)
	require.NoError(t, err)

	delivered := make(chan int, 8)
	var mu sync.Mutex
	count := 0
	fake := &fakeSink{
		name: "fake-sink",
		send: func(ctx context.Context, row map[string]any) error {
			mu.Lock()
			defer mu.Unlock()
			count++
			delivered <- count
			return nil
		},
	}
	w := auditworker.New(auditworker.Config{
		Pool: pool, Sinks: []auditworker.Sink{fake},
		MaxAttempts: 3, BackoffStart: 50 * time.Millisecond,
	})
	go w.Run(ctx)

	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("sink never received row")
	}
}

func TestWorkerRetriesThenDLQs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool := newWorkerPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	writer := audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"})
	require.NoError(t, writer.Write(ctx, audit.Event{
		Action:   "AUTH_SIGNIN_SUCCESS",
		Severity: audit.SeverityInfo,
		Actor:    audit.Actor{Type: audit.ActorUser},
	}))
	_, err := pool.Exec(ctx,
		`INSERT INTO public.audit_outbox(audit_id, audit_ts, sink)
		   SELECT id, ts, 'always-fail'
		     FROM public.audit_log
		    WHERE action='AUTH_SIGNIN_SUCCESS'
		    ORDER BY ts DESC
		    LIMIT 1`)
	require.NoError(t, err)

	failer := &fakeSink{
		name: "always-fail",
		send: func(ctx context.Context, row map[string]any) error { return errors.New("nope") },
	}
	w := auditworker.New(auditworker.Config{
		Pool: pool, Sinks: []auditworker.Sink{failer},
		MaxAttempts: 2, BackoffStart: 10 * time.Millisecond,
	})
	go w.Run(ctx)

	require.Eventually(t, func() bool {
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_outbox_dlq WHERE sink='always-fail'`).Scan(&n)
		return n > 0
	}, 5*time.Second, 100*time.Millisecond)
}

type fakeSink struct {
	name string
	send func(ctx context.Context, row map[string]any) error
}

func (f *fakeSink) Name() string { return f.name }
func (f *fakeSink) Send(ctx context.Context, row map[string]any) error {
	return f.send(ctx, row)
}

func newWorkerPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool
}
