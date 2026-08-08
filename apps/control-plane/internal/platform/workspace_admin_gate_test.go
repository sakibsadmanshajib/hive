package platform_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// stubTenantStore is the test double for platform.TenantRoleStore, the
// tenant-keyed role table public.tenant_users.
type stubTenantStore struct {
	roles map[tenantKey]platform.TenantRole
	err   error
}

type tenantKey struct {
	user   uuid.UUID
	tenant uuid.UUID
}

func newStubTenantStore() *stubTenantStore {
	return &stubTenantStore{roles: make(map[tenantKey]platform.TenantRole)}
}

func (s *stubTenantStore) GetTenantRole(ctx context.Context, userID, tenantID uuid.UUID) (platform.TenantRole, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.roles[tenantKey{userID, tenantID}], nil
}

// gateFixture wires the gate under test to a terminal handler that records
// whether it ran and which overlay the gate stamped into the context.
type gateFixture struct {
	handler  http.Handler
	served   bool
	sawAdmin bool
}

func newGateFixture(tenants *stubTenantStore, roles *stubStore) *gateFixture {
	f := &gateFixture{}
	gate := platform.NewWorkspaceAdminGate(
		platform.NewTenantRoleService(tenants),
		platform.NewRoleService(roles),
	)
	f.handler = gate.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.served = true
		f.sawAdmin = platform.PlatformAdminFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	return f
}

// requestAs builds a request carrying viewer v, as auth.Middleware would.
func requestAs(v auth.Viewer) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/feature-gates", nil)
	return req.WithContext(auth.WithViewer(req.Context(), v))
}

// The workspace OWNER reaches the workspace-scoped admin surface with no
// platform-admin flag anywhere, which is the whole point of issue #758: the
// demo account owns its workspace and is deliberately not a platform admin.
func TestWorkspaceAdminGate_TenantOwnerPasses(t *testing.T) {
	owner := uuid.New()
	tenant := uuid.New()
	tenants := newStubTenantStore()
	tenants.roles[tenantKey{owner, tenant}] = platform.TenantRoleOwner

	f := newGateFixture(tenants, newStubStore())
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, requestAs(auth.Viewer{UserID: owner, TenantID: tenant}))

	if rec.Code != http.StatusOK {
		t.Fatalf("owner got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !f.served {
		t.Fatal("owner did not reach the handler")
	}
	if f.sawAdmin {
		t.Error("owner was stamped as a platform admin")
	}
}

// Cross-workspace isolation. The caller is a real OWNER, of a different tenant.
// Selecting tenant B must not carry authority from tenant A, which is the leak
// shape issue #750 describes for the account-scoped platform-admin overlay.
func TestWorkspaceAdminGate_OwnerOfAnotherWorkspaceDenied(t *testing.T) {
	owner := uuid.New()
	tenantA := uuid.New()
	tenantB := uuid.New()
	tenants := newStubTenantStore()
	tenants.roles[tenantKey{owner, tenantA}] = platform.TenantRoleOwner

	f := newGateFixture(tenants, newStubStore())
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, requestAs(auth.Viewer{UserID: owner, TenantID: tenantB}))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("owner of another workspace got %d, want 403", rec.Code)
	}
	if f.served {
		t.Fatal("owner of another workspace reached the handler")
	}
}

// A plain member of the tenant in scope administers nothing.
func TestWorkspaceAdminGate_MemberDenied(t *testing.T) {
	member := uuid.New()
	tenant := uuid.New()
	tenants := newStubTenantStore()
	tenants.roles[tenantKey{member, tenant}] = platform.TenantRole("MEMBER")

	f := newGateFixture(tenants, newStubStore())
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, requestAs(auth.Viewer{UserID: member, TenantID: tenant}))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("member got %d, want 403", rec.Code)
	}
	if f.served {
		t.Fatal("member reached the handler")
	}
}

// A platform admin keeps its reach and is stamped, which is what lets the
// handlers behind this gate keep their platform-only carve-outs working.
func TestWorkspaceAdminGate_PlatformAdminPassesAndIsStamped(t *testing.T) {
	admin := uuid.New()
	roles := newStubStore()
	roles.platAdmins[admin] = true

	f := newGateFixture(newStubTenantStore(), roles)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, requestAs(auth.Viewer{UserID: admin, TenantID: uuid.New()}))

	if rec.Code != http.StatusOK {
		t.Fatalf("platform admin got %d, want 200", rec.Code)
	}
	if !f.sawAdmin {
		t.Error("platform admin was not stamped into the request context")
	}
}

func TestWorkspaceAdminGate_UnauthenticatedGets401(t *testing.T) {
	f := newGateFixture(newStubTenantStore(), newStubStore())
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/feature-gates", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated got %d, want 401", rec.Code)
	}
}

func TestWorkspaceAdminGate_NoTenantSelectedGets400(t *testing.T) {
	f := newGateFixture(newStubTenantStore(), newStubStore())
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, requestAs(auth.Viewer{UserID: uuid.New()}))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("tenantless viewer got %d, want 400", rec.Code)
	}
	if f.served {
		t.Fatal("tenantless viewer reached the handler")
	}
}

// A failed role lookup denies rather than admits.
func TestWorkspaceAdminGate_LookupErrorGets500(t *testing.T) {
	tenants := newStubTenantStore()
	tenants.err = errors.New("boom")

	f := newGateFixture(tenants, newStubStore())
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, requestAs(auth.Viewer{UserID: uuid.New(), TenantID: uuid.New()}))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("lookup error got %d, want 500", rec.Code)
	}
	if f.served {
		t.Fatal("request reached the handler despite a lookup error")
	}
}
