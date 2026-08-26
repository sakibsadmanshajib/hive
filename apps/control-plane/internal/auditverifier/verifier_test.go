package auditverifier_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auditverifier"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestVerifierChainOKReturnsNoMismatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool := newVerifierPool(t, ctx)
	t.Cleanup(func() { pool.Close() })
	resetAuditLog(t, ctx, pool)

	writer := audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"})
	for i := 0; i < 3; i++ {
		require.NoError(t, writer.Write(ctx, audit.Event{
			Action:   "AUTH_SIGNIN_SUCCESS",
			Severity: audit.SeverityInfo,
			Actor:    audit.Actor{Type: audit.ActorUser},
		}))
	}

	v := auditverifier.New(pool)
	mismatches, err := v.VerifyPartition(ctx, time.Now())
	require.NoError(t, err)
	require.Equal(t, 0, mismatches)
}

func TestVerifierTamperedRowDetected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool := newVerifierPool(t, ctx)
	t.Cleanup(func() { pool.Close() })
	resetAuditLog(t, ctx, pool)

	writer := audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"})
	require.NoError(t, writer.Write(ctx, audit.Event{
		Action:   "AUTH_SIGNIN_SUCCESS",
		Severity: audit.SeverityInfo,
		Actor:    audit.Actor{Type: audit.ActorUser},
	}))
	require.NoError(t, writer.Write(ctx, audit.Event{
		Action:   "RBAC_DENY",
		Severity: audit.SeverityWarning,
		Actor:    audit.Actor{Type: audit.ActorUser},
	}))

	_, err := pool.Exec(ctx, `
		WITH first AS (SELECT id, ts FROM public.audit_log ORDER BY seq LIMIT 1)
		UPDATE public.audit_log
		   SET row_hash = decode('00','hex') || substring(row_hash from 2)
		 WHERE id = (SELECT id FROM first)
		   AND ts = (SELECT ts FROM first)`)
	if err != nil {
		// Same loudness contract as the DSN gate: a tamper UPDATE failing in CI
		// means privilege or policy wiring regressed; never a normal day.
		if os.Getenv("CI") != "" && !testing.Short() {
			t.Fatal("tamper UPDATE failed in CI: owner privilege or audit_log policy regressed")
		}
		t.Skip("tamper requires owner privilege on test DB")
	}

	v := auditverifier.New(pool)
	mismatches, err := v.VerifyPartition(ctx, time.Now())
	require.NoError(t, err)
	require.GreaterOrEqual(t, mismatches, 1)
}

// resetAuditLog makes the live tests hermetic on a shared sequential-run
// database: the CI live leg runs every package against ONE Postgres with
// -p 1, so this suite's whole-partition "zero mismatches" assertion and the
// first-row-by-seq tamper UPDATE both see rows other suites left behind. The
// tamper test corrupted another tenant's chain and broke this suite the first
// time it ever actually ran (found by the 2026-08-26 un-skip pass); it had
// been silently skipping everywhere before that.
func resetAuditLog(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, "DELETE FROM public.audit_log"); err != nil {
		t.Fatalf("reset audit_log: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM public.audit_log")
	})
}

func newVerifierPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
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
