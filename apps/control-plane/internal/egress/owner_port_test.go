package egress_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/egress"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// public.egress_policies is keyed by tenant_id, so the write gate has to read
// authority from public.tenant_users. It was wired to the account-scoped
// platform.RoleService instead, whose IsWorkspaceOwner resolves its id against
// public.accounts: a tenant id never exists there, the store returned
// ErrWorkspaceNotFound, and every /api/v1/egress-policy/ request answered 500.
// The mistake compiled cleanly because both predicates took two bare UUIDs.
//
// These two assertions are the guard. The first pins the correct type to the
// port; the second fails if the port is ever widened back to a shape the
// account-scoped service also satisfies.

var _ egress.OwnerChecker = (*platform.TenantRoleService)(nil)

func TestAccountScopedRoleServiceDoesNotSatisfyOwnerChecker(t *testing.T) {
	var accountScoped any = (*platform.RoleService)(nil)
	if _, ok := accountScoped.(egress.OwnerChecker); ok {
		t.Fatal("platform.RoleService satisfies egress.OwnerChecker: the egress write gate is " +
			"account-scoped again and cannot authorize a tenant id")
	}
}

// stubTenantRoleStore returns a fixed tenant_users role.
type stubTenantRoleStore struct {
	role platform.TenantRole
	err  error
}

func (s stubTenantRoleStore) GetTenantRole(context.Context, uuid.UUID, uuid.UUID) (platform.TenantRole, error) {
	return s.role, s.err
}

func TestTenantRoleServiceIsTenantOwner(t *testing.T) {
	cases := []struct {
		name string
		role platform.TenantRole
		want bool
	}{
		{"owner", platform.TenantRoleOwner, true},
		{"admin is not owner", platform.TenantRoleAdmin, false},
		{"member is not owner", platform.TenantRole("MEMBER"), false},
		{"no membership", platform.TenantRole(""), false},
		{"lowercase account-scoped value is not owner", platform.TenantRole("owner"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := platform.NewTenantRoleService(stubTenantRoleStore{role: tc.role})
			got, err := svc.IsTenantOwner(context.Background(), uuid.New(), uuid.New())
			if err != nil {
				t.Fatalf("IsTenantOwner: %v", err)
			}
			if got != tc.want {
				t.Fatalf("role %q: got %v, want %v", tc.role, got, tc.want)
			}
		})
	}
}

func TestTenantRoleServicePropagatesStoreError(t *testing.T) {
	svc := platform.NewTenantRoleService(stubTenantRoleStore{err: context.DeadlineExceeded})
	if _, err := svc.IsTenantOwner(context.Background(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("expected the store error to propagate, got nil")
	}
}
