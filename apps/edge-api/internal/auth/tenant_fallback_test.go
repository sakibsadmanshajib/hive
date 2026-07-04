package auth_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

// newPool mirrors internal/chat/dispatch_test.go's helper of the same
// name: it skips the test rather than failing when no test DB is wired
// up, so these DB-backed branches only run where HIVE_TEST_DB_URL is set
// (CI integration job / local docker-compose test profile).
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

// TestTenantFallback_NilPool_ReportsNotFound verifies that a fallback
// backed by a nil pool (DB not configured for this deployment) always
// reports "not found" rather than panicking or erroring -- JWTMiddleware
// relies on this to keep failing closed when no DB is wired up.
func TestTenantFallback_NilPool_ReportsNotFound(t *testing.T) {
	f := auth.NewTenantFallback(nil)
	tenantID, role, ok, err := f.Resolve(context.Background(), uuid.New())
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, uuid.Nil, tenantID)
	require.Empty(t, role)
}

// TestTenantFallback_NilReceiver_ReportsNotFound covers a *TenantFallback
// that is itself nil (e.g. a caller that never wired one up), since
// JWTMiddleware calls tenantFallback.Resolve unconditionally.
func TestTenantFallback_NilReceiver_ReportsNotFound(t *testing.T) {
	var f *auth.TenantFallback
	tenantID, role, ok, err := f.Resolve(context.Background(), uuid.New())
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, uuid.Nil, tenantID)
	require.Empty(t, role)
}

// TestTenantFallback_NoActiveMembership_ReportsNotFound exercises the real
// pgx.ErrNoRows branch (a genuine DB round trip that finds no active,
// unarchived membership) as distinct from the nil-pool/nil-receiver short
// circuits above -- both must report "not found" with no error, but only
// this one actually reaches row.Scan.
func TestTenantFallback_NoActiveMembership_ReportsNotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(pool.Close)

	f := auth.NewTenantFallback(pool)
	tenantID, role, ok, err := f.Resolve(ctx, uuid.New()) // random user, no memberships exist
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, uuid.Nil, tenantID)
	require.Empty(t, role)
}

// TestTenantFallback_QueryError_ReturnsError exercises the non-ErrNoRows
// scan-error branch: a canceled context makes the pool fail the query
// outright rather than returning zero rows, so Resolve must propagate the
// error instead of masking it as a clean miss.
func TestTenantFallback_QueryError_ReturnsError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := newPool(t, ctx)
	t.Cleanup(pool.Close)

	canceled, cancelNow := context.WithCancel(ctx)
	cancelNow()

	f := auth.NewTenantFallback(pool)
	tenantID, role, ok, err := f.Resolve(canceled, uuid.New())
	require.Error(t, err)
	require.False(t, ok)
	require.Equal(t, uuid.Nil, tenantID)
	require.Empty(t, role)
}
