package accounts_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// stubPlatformAdminStore is a minimal platform.RoleStore backing a real
// *platform.RoleService for tests, keyed by userID -> is_platform_admin.
type stubPlatformAdminStore struct {
	adminUsers map[uuid.UUID]bool
}

func (s *stubPlatformAdminStore) GetMembershipRole(_ context.Context, _, _ uuid.UUID) (platform.MembershipRole, error) {
	return "", nil
}

func (s *stubPlatformAdminStore) IsPlatformAdmin(_ context.Context, userID uuid.UUID) (bool, error) {
	return s.adminUsers[userID], nil
}

// TestEnsureViewerContext_PlatformAdminOverlay is a regression guard for the
// live bug where GET /api/v1/viewer never included "platform.admin" in
// permissions[] for a real platform admin, because EnsureViewerContext
// hardcoded isAdmin=false regardless of accounts.is_platform_admin. That
// mismatch made the web-console Feature Gates and Marketplace pages (which
// gate purely on permissions[] containing "platform.admin") refuse to render
// for real admins, even though admin-gated backend routes already allowed
// them via a separately-resolved Actor.
func TestEnsureViewerContext_PlatformAdminOverlay(t *testing.T) {
	cases := []struct {
		name        string
		isAdmin     bool
		wantPresent bool
	}{
		{"platform admin gets platform.admin in permissions", true, true},
		{"non admin does not get platform.admin in permissions", false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newStubRepo()
			userID := uuid.New()

			store := &stubPlatformAdminStore{adminUsers: map[uuid.UUID]bool{}}
			if tc.isAdmin {
				store.adminUsers[userID] = true
			}
			roleSvc := platform.NewRoleService(store)

			svc := accounts.NewService(repo).WithRoleService(roleSvc)

			viewer := auth.Viewer{
				UserID:        userID,
				Email:         "admin-check@example.com",
				EmailVerified: true,
				FullName:      "Admin Check",
			}

			vc, err := svc.EnsureViewerContext(context.Background(), viewer, uuid.Nil)
			if err != nil {
				t.Fatalf("EnsureViewerContext error: %v", err)
			}

			present := false
			for _, p := range vc.Permissions {
				if p == "platform.admin" {
					present = true
					break
				}
			}
			if present != tc.wantPresent {
				t.Errorf("platform.admin present: want %v got %v (permissions=%v)", tc.wantPresent, present, vc.Permissions)
			}
		})
	}
}

// TestEnsureViewerContext_NoRoleService_DefaultsToNonAdmin confirms the
// pre-existing behaviour is preserved when WithRoleService is never called
// (nil roleSvc): isAdmin stays false, matching every existing caller/test
// that constructs a Service via NewService alone.
func TestEnsureViewerContext_NoRoleService_DefaultsToNonAdmin(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)

	viewer := auth.Viewer{
		UserID:        uuid.New(),
		Email:         "no-role-svc@example.com",
		EmailVerified: true,
		FullName:      "No Role Svc",
	}

	vc, err := svc.EnsureViewerContext(context.Background(), viewer, uuid.Nil)
	if err != nil {
		t.Fatalf("EnsureViewerContext error: %v", err)
	}

	for _, p := range vc.Permissions {
		if p == "platform.admin" {
			t.Error("expected platform.admin absent when roleSvc is not wired")
		}
	}
}

// TestCreateInvitation_PlatformAdminOverlay is a regression guard for issue
// #424: CreateInvitation hardcoded isAdmin=false when building its Actor, so
// a real platform admin who is a non-owner member of the target account was
// silently denied members.invite even though the admin overlay should grant
// it. A hardcoded-false version returns permission_denied here; the fix must
// succeed.
func TestCreateInvitation_PlatformAdminOverlay(t *testing.T) {
	cases := []struct {
		name    string
		isAdmin bool
		wantErr bool
	}{
		{"platform admin non-owner member is granted members.invite", true, false},
		{"non-admin non-owner member is denied members.invite", false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newStubRepo()
			userID := uuid.New()
			accountID := uuid.New()

			repo.accountsMap[accountID] = &accounts.Account{
				ID:          accountID,
				Slug:        "ws",
				DisplayName: "WS",
				AccountType: "personal",
				OwnerUserID: uuid.New(),
			}
			repo.memberships = []accounts.Membership{
				{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "member", Status: "active"},
			}

			store := &stubPlatformAdminStore{adminUsers: map[uuid.UUID]bool{}}
			if tc.isAdmin {
				store.adminUsers[userID] = true
			}
			roleSvc := platform.NewRoleService(store)

			svc := accounts.NewService(repo).WithRoleService(roleSvc)

			viewer := auth.Viewer{
				UserID:        userID,
				Email:         "admin-check@example.com",
				EmailVerified: false,
			}

			_, err := svc.CreateInvitation(context.Background(), accountID, viewer, "invitee@example.com")
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
		})
	}
}
