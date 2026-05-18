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
		t.Skip("tamper requires owner privilege on test DB")
	}

	v := auditverifier.New(pool)
	mismatches, err := v.VerifyPartition(ctx, time.Now())
	require.NoError(t, err)
	require.GreaterOrEqual(t, mismatches, 1)
}

func newVerifierPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool
}
