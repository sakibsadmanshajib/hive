package platform_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
	"github.com/hivegpt/hive/apps/control-plane/internal/platform"
)

// stubStore is the test double for platform.RoleStore. It is intentionally
// small — interface placement follows accept-interfaces-return-structs.
type stubStore struct {
	memberships map[membershipKey]platform.MembershipRole
	missingWS   map[uuid.UUID]bool
	platAdmins  map[uuid.UUID]bool
	getErr      error
	adminErr    error
}

type membershipKey struct {
	user      uuid.UUID
	workspace uuid.UUID
}

func newStubStore() *stubStore {
	return &stubStore{
		memberships: make(map[membershipKey]platform.MembershipRole),
		missingWS:   make(map[uuid.UUID]bool),
		platAdmins:  make(map[uuid.UUID]bool),
	}
}

func (s *stubStore) GetMembershipRole(ctx context.Context, userID, workspaceID uuid.UUID) (platform.MembershipRole, error) {
	if s.getErr != nil {
		return "", s.getErr
	}
	if s.missingWS[workspaceID] {
		return "", platform.ErrWorkspaceNotFound
	}
	return s.memberships[membershipKey{userID, workspaceID}], nil
}

func (s *stubStore) IsPlatformAdmin(ctx context.Context, userID uuid.UUID) (bool, error) {
	if s.adminErr != nil {
		return false, s.adminErr
	}
	return s.platAdmins[userID], nil
}

// ----------------------------------------------------------------------------
// IsWorkspaceOwner — covers Task 2 behavior cases (PLAN.md lines 458-461)
// ----------------------------------------------------------------------------

func TestIsWorkspaceOwner_OwnerRoleReturnsTrue(t *testing.T) {
	t.Parallel()
	store := newStubStore()
	owner := uuid.New()
	ws := uuid.New()
	store.memberships[membershipKey{owner, ws}] = platform.RoleOwner

	svc := platform.NewRoleService(store)

	got, err := svc.IsWorkspaceOwner(context.Background(), owner, ws)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Fatalf("expected true for owner role, got false")
	}
}

func TestIsWorkspaceOwner_MemberRoleReturnsFalse(t *testing.T) {
	t.Parallel()
	store := newStubStore()
	member := uuid.New()
	ws := uuid.New()
	store.memberships[membershipKey{member, ws}] = platform.RoleMember

	svc := platform.NewRoleService(store)

	got, err := svc.IsWorkspaceOwner(context.Background(), member, ws)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Fatalf("expected false for member role, got true")
	}
}

func TestIsWorkspaceOwner_AdminRoleReturnsFalse(t *testing.T) {
	// Phase 14 strictly owner-only for grant scope; Phase 18 will refine.
	// account_memberships.role currently has no 'admin' value (CHECK clause is
	// owner|member), so any non-owner string MUST be treated as not-owner.
	t.Parallel()
	store := newStubStore()
	user := uuid.New()
	ws := uuid.New()
	store.memberships[membershipKey{user, ws}] = platform.MembershipRole("admin")

	svc := platform.NewRoleService(store)

	got, err := svc.IsWorkspaceOwner(context.Background(), user, ws)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Fatalf("expected false for non-owner role 'admin', got true")
	}
}

func TestIsWorkspaceOwner_StrangerReturnsFalse(t *testing.T) {
	// Workspace exists but the user has no membership row.
	t.Parallel()
	store := newStubStore()
	stranger := uuid.New()
	ws := uuid.New()
	// no membership row for (stranger, ws)

	svc := platform.NewRoleService(store)

	got, err := svc.IsWorkspaceOwner(context.Background(), stranger, ws)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Fatalf("expected false for stranger user, got true")
	}
}

func TestIsWorkspaceOwner_MissingWorkspaceReturnsErr(t *testing.T) {
	t.Parallel()
	store := newStubStore()
	user := uuid.New()
	missingWS := uuid.New()
	store.missingWS[missingWS] = true

	svc := platform.NewRoleService(store)

	got, err := svc.IsWorkspaceOwner(context.Background(), user, missingWS)
	if !errors.Is(err, platform.ErrWorkspaceNotFound) {
		t.Fatalf("expected ErrWorkspaceNotFound, got %v", err)
	}
	if got {
		t.Fatalf("expected false on missing-workspace error path, got true")
	}
}

func TestIsWorkspaceOwner_PropagatesStoreError(t *testing.T) {
	t.Parallel()
	store := newStubStore()
	store.getErr = errors.New("connection refused")

	svc := platform.NewRoleService(store)

	got, err := svc.IsWorkspaceOwner(context.Background(), uuid.New(), uuid.New())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if got {
		t.Fatalf("expected false on store error, got true")
	}
}

// ----------------------------------------------------------------------------
// IsPlatformAdmin
// ----------------------------------------------------------------------------

func TestIsPlatformAdmin_FlaggedUserReturnsTrue(t *testing.T) {
	t.Parallel()
	store := newStubStore()
	admin := uuid.New()
	store.platAdmins[admin] = true

	svc := platform.NewRoleService(store)

	got, err := svc.IsPlatformAdmin(context.Background(), admin)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Fatalf("expected true for platform admin, got false")
	}
}

func TestIsPlatformAdmin_DefaultUserReturnsFalse(t *testing.T) {
	t.Parallel()
	store := newStubStore()
	svc := platform.NewRoleService(store)

	got, err := svc.IsPlatformAdmin(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Fatalf("expected false for default user, got true")
	}
}

// ----------------------------------------------------------------------------
// RequirePlatformAdmin middleware
// ----------------------------------------------------------------------------

func TestRequirePlatformAdmin_AdminPasses(t *testing.T) {
	t.Parallel()
	store := newStubStore()
	admin := uuid.New()
	store.platAdmins[admin] = true
	svc := platform.NewRoleService(store)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/grants", nil)
	ctx := auth.WithViewer(req.Context(), auth.Viewer{UserID: admin})
	req = req.WithContext(ctx)

	svc.RequirePlatformAdmin(next).ServeHTTP(rec, req)

	if !called {
		t.Fatalf("expected next handler to be invoked")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestRequirePlatformAdmin_NonAdminGets403(t *testing.T) {
	t.Parallel()
	store := newStubStore()
	user := uuid.New()
	svc := platform.NewRoleService(store)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("next handler must NOT be invoked for non-admin")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/grants", nil)
	req = req.WithContext(auth.WithViewer(req.Context(), auth.Viewer{UserID: user}))

	svc.RequirePlatformAdmin(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	// Provider-blind sanitized JSON — must not leak provider names or stack.
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("response body must be JSON: %v", err)
	}
	if body["error"] == "" {
		t.Fatalf("expected error field in body, got %v", body)
	}
}

func TestRequirePlatformAdmin_UnauthenticatedGets401(t *testing.T) {
	t.Parallel()
	svc := platform.NewRoleService(newStubStore())

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("next handler must NOT be invoked without auth viewer")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/grants", nil)
	// No auth.WithViewer applied.

	svc.RequirePlatformAdmin(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestRequirePlatformAdmin_StoreErrorReturns500(t *testing.T) {
	t.Parallel()
	store := newStubStore()
	store.adminErr = errors.New("db down")
	svc := platform.NewRoleService(store)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("next handler must NOT be invoked on store error")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/grants", nil)
	req = req.WithContext(auth.WithViewer(req.Context(), auth.Viewer{UserID: uuid.New()}))

	svc.RequirePlatformAdmin(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}
