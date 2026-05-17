package auth_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/hivegpt/hive/apps/edge-api/internal/auth"
)

func TestContext_RoundTrip(t *testing.T) {
	uid := uuid.New()
	tid := uuid.New()
	ctx := auth.WithUser(context.Background(), &auth.User{
		ID:       uid,
		TenantID: tid,
		Role:     "ADMIN",
		Email:    "u@x.example",
	})
	got, ok := auth.UserFrom(ctx)
	require.True(t, ok)
	require.Equal(t, uid, got.ID)
	require.Equal(t, tid, got.TenantID)
	require.Equal(t, "ADMIN", got.Role)
	require.Equal(t, "u@x.example", got.Email)
}

func TestContext_Missing_ReturnsFalse(t *testing.T) {
	got, ok := auth.UserFrom(context.Background())
	require.False(t, ok)
	require.Nil(t, got)
}

func TestTenantID_FromContext(t *testing.T) {
	tid := uuid.New()
	ctx := auth.WithUser(context.Background(), &auth.User{TenantID: tid})
	require.Equal(t, tid, auth.TenantID(ctx))
}

func TestTenantID_MissingContext_ReturnsNil(t *testing.T) {
	require.Equal(t, uuid.Nil, auth.TenantID(context.Background()))
}
