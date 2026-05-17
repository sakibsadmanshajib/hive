package audit_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/hivegpt/hive/apps/control-plane/internal/audit"
)

func TestLog_SecurityTierWritesAndChains(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	w := audit.NewSyncWriter(pool, audit.WriterConfig{
		DeploySHA: "test-sha", Env: "test",
	})

	tid := uuid.New()
	rid := uuid.New()

	err := w.Write(ctx, audit.Event{
		TenantID:  tid,
		Actor:     audit.Actor{ID: uuid.New(), Type: audit.ActorUser},
		Action:    "AUTH_SIGNIN_SUCCESS",
		Severity:  audit.SeverityInfo,
		RequestID: rid,
	})
	require.NoError(t, err)

	err = w.Write(ctx, audit.Event{
		TenantID:  tid,
		Actor:     audit.Actor{ID: uuid.New(), Type: audit.ActorUser},
		Action:    "RBAC_DENY",
		Severity:  audit.SeverityWarning,
		RequestID: rid,
	})
	require.NoError(t, err)

	var seq1, seq2 int64
	var prev1, prev2, row1, row2 []byte
	rows, err := pool.Query(ctx,
		`SELECT seq, prev_hash, row_hash FROM public.audit_log WHERE tenant_id=$1 ORDER BY seq`,
		tid)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&seq1, &prev1, &row1))
	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&seq2, &prev2, &row2))

	require.Equal(t, int64(seq1+1), seq2)
	require.Equal(t, hex.EncodeToString(row1), hex.EncodeToString(prev2),
		"second row's prev_hash must equal first row's row_hash")
	require.NotEqual(t, sha256.Size, 0)
}

func TestLog_DispatchByTierUsesSyncForCritical(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t, context.Background())
	t.Cleanup(func() { pool.Close() })

	logger := audit.NewLogger(audit.LoggerDeps{
		Sync: audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"}),
		WAL:  &countingWAL{},
	})

	err := logger.Log(ctx, audit.Event{
		Action:   "CROSS_TENANT_ATTEMPT",
		Severity: audit.SeverityCritical,
		Actor:    audit.Actor{Type: audit.ActorSystem},
	})
	require.NoError(t, err)

	require.Equal(t, 0, logger.Deps().WAL.(*countingWAL).count,
		"CRITICAL must NOT touch the WAL path")
}

type countingWAL struct{ count int }

func (w *countingWAL) Write(ctx context.Context, e audit.Event) error { w.count++; return nil }

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
