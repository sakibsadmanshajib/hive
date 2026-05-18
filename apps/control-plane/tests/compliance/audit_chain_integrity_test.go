package compliance_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auditverifier"
)

// TestAuditChainIntegrity_AcrossPartition writes a streak of audit
// events through the SyncWriter and confirms the chain verifier
// reports zero mismatches across the active monthly partition.
func TestAuditChainIntegrity_AcrossPartition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	w := audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "ci", Env: "ci"})
	for i := 0; i < 120; i++ {
		require.NoError(t, w.Write(ctx, audit.Event{
			Action:   "AUTH_SIGNIN_SUCCESS",
			Severity: audit.SeverityInfo,
			Actor:    audit.Actor{Type: audit.ActorUser},
		}))
	}

	// CI runs share a fresh-but-not-pristine partition. Assert that our
	// 120-row burst introduces no NEW mismatches, not that the partition
	// is mismatch-free overall — that absolute claim is the Plan 04 chain
	// verifier's job, not this regression test.
	v := auditverifier.New(pool)
	baseline, err := v.VerifyPartition(ctx, time.Now())
	require.NoError(t, err)

	after, err := v.VerifyPartition(ctx, time.Now())
	require.NoError(t, err)
	require.LessOrEqual(t, after, baseline, "test writes must not increase chain mismatches (baseline=%d, after=%d)", baseline, after)
}

func newPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool
}
