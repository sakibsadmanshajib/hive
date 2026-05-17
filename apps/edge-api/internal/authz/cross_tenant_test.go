package authz_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/hivegpt/hive/apps/edge-api/internal/auth"
	"github.com/hivegpt/hive/apps/edge-api/internal/authz"
)

func TestRequireOwnTenant_SameTenant_NilError(t *testing.T) {
	tid := uuid.New()
	ctx := auth.WithUser(context.Background(), &auth.User{TenantID: tid})
	logged := false
	require.NoError(t, authz.RequireOwnTenant(ctx, tid, func(action string) {
		logged = true
		_ = action
	}))
	require.False(t, logged, "audit callback must not fire on same-tenant access")
}

func TestRequireOwnTenant_DifferentTenant_AuditsAndReturnsForbidden(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	ctx := auth.WithUser(context.Background(), &auth.User{TenantID: a})
	logged := ""
	err := authz.RequireOwnTenant(ctx, b, func(action string) { logged = action })
	require.ErrorIs(t, err, authz.ErrForbidden)
	require.Equal(t, "CROSS_TENANT_ATTEMPT", logged)
}

func TestRequireOwnTenant_MissingUserContext_AuditsAndReturnsForbidden(t *testing.T) {
	requested := uuid.New()
	logged := ""
	err := authz.RequireOwnTenant(context.Background(), requested, func(action string) { logged = action })
	require.ErrorIs(t, err, authz.ErrForbidden)
	require.Equal(t, "CROSS_TENANT_ATTEMPT", logged)
}

func TestRequireOwnTenant_NilTenantOnContext_AuditsAndReturnsForbidden(t *testing.T) {
	ctx := auth.WithUser(context.Background(), &auth.User{TenantID: uuid.Nil})
	requested := uuid.New()
	logged := ""
	err := authz.RequireOwnTenant(ctx, requested, func(action string) { logged = action })
	require.ErrorIs(t, err, authz.ErrForbidden)
	require.Equal(t, "CROSS_TENANT_ATTEMPT", logged)
}

func TestRequireOwnTenant_BothNilTenants_StillDenied(t *testing.T) {
	// Defence in depth: even when both are uuid.Nil, deny — uuid.Nil is
	// never a legitimate tenant id.
	ctx := auth.WithUser(context.Background(), &auth.User{TenantID: uuid.Nil})
	logged := ""
	err := authz.RequireOwnTenant(ctx, uuid.Nil, func(action string) { logged = action })
	require.ErrorIs(t, err, authz.ErrForbidden)
	require.Equal(t, "CROSS_TENANT_ATTEMPT", logged)
}

func TestRequireOwnTenant_NilAuditFunc_DoesNotPanic(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	ctx := auth.WithUser(context.Background(), &auth.User{TenantID: a})
	require.NotPanics(t, func() {
		err := authz.RequireOwnTenant(ctx, b, nil)
		require.ErrorIs(t, err, authz.ErrForbidden)
	})
}
