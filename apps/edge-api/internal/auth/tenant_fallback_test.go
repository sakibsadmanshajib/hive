package auth_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

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
