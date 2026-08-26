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
// caller-level retry, and asserts EXACTLY N rows land — not "at least one" —
// AND that the writer's advisory lock is fully released afterward, not just
// that N writes were fast enough to look serialized.
//
// Before #1182's fix, this reproduces the diagnosed failure directly: the
// SERIALIZABLE snapshot inside writeOnce was taken at the first statement
// after BeginTx (the old pg_advisory_xact_lock call), which is BEFORE that
// lock is actually granted, so concurrent writers blocked on the lock each
// already held a snapshot that predates their turn. Postgres SSI aborts the
// COMMIT with 40001 once it detects the resulting write skew on the shared
// MAX(seq) read, and the writer's own 3-attempt retry is not enough to cover
// 10-way full concurrency.
//
// The row-count assertion alone proves throughput, not release: ten
// sequential-ish writes through a leaked-but-somehow-reacquired lock could
// still land ten rows. The pg_locks assertion at the end is what actually
// tests the risk this PR carries (a held session-scoped lock never being
// unlocked) rather than a proxy for it (#1188 review thread).
func TestSyncWriter_ConcurrentWrites_ExactlyNRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := newSyncConcurrencyPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	w := audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "concurrency-test", Env: "test"})

	tenantID := uuid.New()
	actorID := uuid.New()
	const n = 10
	// Captured once, before any write, so every write in this run is
	// expected to land in the same UTC month bucket and the pg_locks
	// check below queries the exact key writeOnce used.
	lockKey := audit.MonthLockKey(time.Now())

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

	// By the time wg.Wait() returned above, every goroutine's w.Write()
	// call had already returned — which only happens after writeOnce's
	// deferred pg_advisory_unlock and conn.Release() have both run. So
	// THIS test's own writes cannot be the source of a lock still held
	// below. What CAN legitimately still be held for a few more
	// milliseconds: the lock key is coarse-grained (per UTC calendar
	// month, not per test, not per package), and `go test ./a/... ./b/...`
	// runs different packages' test binaries concurrently by default.
	// internal/tenants' own Switch concurrency tests also drive real
	// audit.SyncWriter writes against this same live Postgres, land in
	// the same month's key, and can genuinely be mid-write at the exact
	// instant this check runs — that's the lock doing its job for a
	// DIFFERENT writer, not this one leaking. A real leak never clears;
	// a neighboring package's in-flight write clears within one write's
	// duration (tens of milliseconds per the timings above), so poll
	// briefly instead of checking once. pg_locks splits a single-bigint
	// advisory lock into its high/low 32 bits as classid/objid with
	// objsubid=1 (see PostgreSQL docs, System Catalogs > pg_locks).
	highBits := lockKey >> 32
	lowBits := lockKey & 0xFFFFFFFF
	var heldLocks int
	deadline := time.Now().Add(2 * time.Second)
	for {
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_locks
			  WHERE locktype = 'advisory' AND objsubid = 1
			    AND classid::bigint = $1 AND objid::bigint = $2`,
			highBits, lowBits).Scan(&heldLocks))
		if heldLocks == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.Zero(t, heldLocks,
		"advisory lock %d must be fully released within 2s of every write returning; a nonzero count means SyncWriter leaked the session-scoped lock (#1188 review thread)", lockKey)
}

func newSyncConcurrencyPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		// CI wires HIVE_TEST_DB_URL for the live-Postgres step and passes
		// -short for the step that has none, so a missing DSN there is a wiring
		// defect (the silent-green never-runs shape of issues #701/#708/#797),
		// not a laptop without Postgres. Fail loudly in CI live leg; local runs
		// without a test database still skip.
		if os.Getenv("CI") != "" && !testing.Short() {
			t.Fatal("HIVE_TEST_DB_URL not set in CI: this suite guards a real-SQL proof and must not silently skip")
		}
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool
}
