package audit_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
)

// TestSyncWriter_ConcurrentWrites_ExactlyNRows drives N fully simultaneous
// SyncWriter.Write calls straight at the writer, with no HTTP layer and no
// caller-level retry, and asserts EXACTLY N rows land — not "at least one".
//
// Before #1182's fix, this reproduces the diagnosed failure directly: the
// SERIALIZABLE snapshot inside writeOnce was taken at the first statement
// after BeginTx (the old pg_advisory_xact_lock call), which is BEFORE that
// lock is actually granted, so concurrent writers blocked on the lock each
// already held a snapshot that predates their turn. Postgres SSI aborts the
// COMMIT with 40001 once it detects the resulting write skew on the shared
// MAX(seq) read, and the writer's own 3-attempt retry is not enough to cover
// 10-way full concurrency.
func TestSyncWriter_ConcurrentWrites_ExactlyNRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := newSyncConcurrencyPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	w := audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "concurrency-test", Env: "test"})

	tenantID := uuid.New()
	actorID := uuid.New()
	const n = 10

	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			errs[idx] = w.Write(ctx, audit.Event{
				TenantID:  tenantID,
				Actor:     audit.Actor{ID: actorID, Type: audit.ActorUser},
				Action:    audit.ActionCrossTenantAttempt,
				Severity:  audit.SeverityCritical,
				RequestID: uuid.New(),
			})
		}(i)
	}
	close(start)
	wg.Wait()

	failed := 0
	for i, err := range errs {
		if err != nil {
			failed++
			t.Logf("write %d failed: %v", i, err)
		}
	}
	require.Zero(t, failed, "%d of %d fully-simultaneous audit.SyncWriter.Write calls failed; N simultaneous writes must produce N rows (#1182)", failed, n)

	var rowCount int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM public.audit_log WHERE tenant_id=$1 AND actor_id=$2 AND action='CROSS_TENANT_ATTEMPT'`,
		tenantID, actorID).Scan(&rowCount))
	require.Equal(t, n, rowCount, "N fully-simultaneous writes must produce exactly N audit rows, not merely at-least-one")
}

func newSyncConcurrencyPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool
}
