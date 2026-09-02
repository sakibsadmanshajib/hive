package accounts_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// Issue #1660. GET /api/v1/viewer used to report only account_memberships.role,
// so the console decided the workspace-administration surfaces (feature gates,
// marketplace) from a table the control-plane does not gate them on. A personal
// tenant's sole owner is 'owner' in account_memberships and 'MEMBER' in
// tenant_users by deliberate design (signup.insertPersonalMembership), so the
// console showed them a page whose data fetch WorkspaceAdminGate then answered
// 403, landing them on an empty state that told them to ask an administrator
// who does not exist.
//
// The fix gives the console the same signal the gate reads: workspace_admin,
// resolved from public.tenant_users for the tenant the caller has selected,
// widened by the platform-admin overlay exactly as WorkspaceAdminGate widens it.

type tenantMember struct {
	user   uuid.UUID
	tenant uuid.UUID
}

// stubTenantRoleStore is a minimal platform.TenantRoleStore backing a real
// *platform.TenantRoleService, keyed by (user, tenant) -> role.
type stubTenantRoleStore struct {
	roles map[tenantMember]platform.TenantRole
	err   error
}

func (s *stubTenantRoleStore) GetTenantRole(_ context.Context, userID, tenantID uuid.UUID) (platform.TenantRole, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.roles[tenantMember{user: userID, tenant: tenantID}], nil
}

// seedOwnerOfAccount gives userID an ACTIVE 'owner' account_memberships row, the
// state every personal-tenant signup reaches, so EnsureViewerContext resolves a
// current account without provisioning one.
func seedOwnerOfAccount(repo *stubRepo, userID, accountID uuid.UUID) {
	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "personal-workspace",
		DisplayName: "Personal workspace",
		AccountType: "personal",
		OwnerUserID: userID,
	}
	repo.memberships = append(repo.memberships, accounts.Membership{
		ID:        uuid.New(),
		AccountID: accountID,
		UserID:    userID,
		Role:      "owner",
		Status:    "active",
	})
}

func TestEnsureViewerContext_WorkspaceAdminMirrorsTenantRole(t *testing.T) {
	cases := []struct {
		name          string
		tenantRole    platform.TenantRole
		platformAdmin bool
		noTenant      bool
		want          bool
	}{
		{
			name:       "personal tenant sole owner is not a workspace administrator",
			tenantRole: platform.TenantRole("MEMBER"),
			want:       false,
		},
		{
			name:       "tenant owner is a workspace administrator",
			tenantRole: platform.TenantRoleOwner,
			want:       true,
		},
		{
			name:       "no tenant_users row at all is not a workspace administrator",
			tenantRole: platform.TenantRole(""),
			want:       false,
		},
		{
			name:          "platform admin overlay widens it exactly as the gate does",
			tenantRole:    platform.TenantRole("MEMBER"),
			platformAdmin: true,
			want:          true,
		},
		{
			name:       "no tenant selected is not a workspace administrator",
			tenantRole: platform.TenantRoleOwner,
			noTenant:   true,
			want:       false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newStubRepo()
			userID := uuid.New()
			accountID := uuid.New()
			tenantID := uuid.New()
			seedOwnerOfAccount(repo, userID, accountID)

			adminStore := &stubPlatformAdminStore{adminUsers: map[uuid.UUID]bool{}}
			if tc.platformAdmin {
				adminStore.adminUsers[userID] = true
			}
			tenantStore := &stubTenantRoleStore{
				roles: map[tenantMember]platform.TenantRole{
					{user: userID, tenant: tenantID}: tc.tenantRole,
				},
			}

			svc := accounts.NewService(repo).
				WithRoleService(platform.NewRoleService(adminStore)).
				WithTenantRoleService(platform.NewTenantRoleService(tenantStore))

			viewer := auth.Viewer{
				UserID:        userID,
				TenantID:      tenantID,
				Email:         "solo-owner@example.com",
				EmailVerified: true,
			}
			if tc.noTenant {
				viewer.TenantID = uuid.Nil
			}

			vc, err := svc.EnsureViewerContext(context.Background(), viewer, accountID)
			if err != nil {
				t.Fatalf("EnsureViewerContext error: %v", err)
			}
			if vc.WorkspaceAdmin != tc.want {
				t.Errorf("WorkspaceAdmin: want %v got %v (account role %q)",
					tc.want, vc.WorkspaceAdmin, vc.CurrentAccount.Role)
			}
		})
	}
}

// A Service with no tenant role service wired (every existing unit test, and
// any deployment that has not wired one) must report false rather than fall
// back to account_memberships.role, which is the exact conflation this fixes.
func TestEnsureViewerContext_NoTenantRoleService_IsNotWorkspaceAdmin(t *testing.T) {
	repo := newStubRepo()
	userID := uuid.New()
	accountID := uuid.New()
	seedOwnerOfAccount(repo, userID, accountID)

	svc := accounts.NewService(repo)
	vc, err := svc.EnsureViewerContext(context.Background(), auth.Viewer{
		UserID:        userID,
		TenantID:      uuid.New(),
		Email:         "no-tenant-svc@example.com",
		EmailVerified: true,
	}, accountID)
	if err != nil {
		t.Fatalf("EnsureViewerContext error: %v", err)
	}
	if vc.WorkspaceAdmin {
		t.Error("WorkspaceAdmin must be false when no tenant role service is wired")
	}
}

// The console reads this off the wire, so the field has to survive the JSON
// encoding, not merely exist on the struct.
func TestViewerHandler_EmitsWorkspaceAdmin(t *testing.T) {
	cases := []struct {
		name       string
		tenantRole platform.TenantRole
		want       bool
	}{
		{"personal tenant sole owner", platform.TenantRole("MEMBER"), false},
		{"tenant owner", platform.TenantRoleOwner, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newStubRepo()
			userID := uuid.New()
			accountID := uuid.New()
			tenantID := uuid.New()
			seedOwnerOfAccount(repo, userID, accountID)

			tenantStore := &stubTenantRoleStore{
				roles: map[tenantMember]platform.TenantRole{
					{user: userID, tenant: tenantID}: tc.tenantRole,
				},
			}
			svc := accounts.NewService(repo).
				WithTenantRoleService(platform.NewTenantRoleService(tenantStore))
			h := accounts.NewHandler(svc)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/viewer", nil)
			req = req.WithContext(auth.WithViewer(context.Background(), auth.Viewer{
				UserID:        userID,
				TenantID:      tenantID,
				Email:         "wire-shape@example.com",
				EmailVerified: true,
			}))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
			}
			var resp map[string]interface{}
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			got, ok := resp["workspace_admin"].(bool)
			if !ok {
				t.Fatalf("response missing boolean 'workspace_admin': %s", rr.Body.String())
			}
			if got != tc.want {
				t.Errorf("workspace_admin: want %v got %v", tc.want, got)
			}
		})
	}
}
