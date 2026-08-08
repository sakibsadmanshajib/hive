package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/marketplace"
)

// Issue #758, security review of PR #788. platform.WorkspaceAdminGate has its
// own unit tests, but nothing proved the gate was still installed on the two
// mounts it guards: deleting cfg.WorkspaceAdminGate.Require from either mount
// left the whole control-plane suite green. That mutant ships an admin surface
// any authenticated MEMBER of the workspace can list and toggle, so these tests
// dispatch through NewRouter and make the mount itself the thing under test.
// Same reasoning as TestNewRouterAdminProvidersRequiresAuth.

// recordingGate stands in for platform.WorkspaceAdminGate. It records that it
// ran and refuses without calling next, so reaching the handler at all is proof
// the gate is missing from that mount.
type recordingGate struct{ calls int }

func (g *recordingGate) Require(http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		g.calls++
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("gate-refused"))
	})
}

// fakeFeatureGateAdmin names the surface a request reached, the same trick
// fakeProvidersRouter uses.
type fakeFeatureGateAdmin struct{}

func (fakeFeatureGateAdmin) AdminMux() http.Handler { return namedHandler("feature-gate-admin-mux") }

// stubMarketplaceRepo satisfies marketplace.Repository so the real handler can
// be mounted: RouterConfig takes the concrete *marketplace.Handler. It answers
// an empty catalog, so a request that slips past the gate returns 200 with a
// JSON body rather than panicking on a nil service.
type stubMarketplaceRepo struct{}

func (stubMarketplaceRepo) ListEntries(context.Context) ([]marketplace.Entry, error) {
	return nil, nil
}

func (stubMarketplaceRepo) GetEntry(context.Context, uuid.UUID) (marketplace.Entry, error) {
	return marketplace.Entry{}, nil
}

func (stubMarketplaceRepo) CreateEntry(_ context.Context, e marketplace.Entry) (marketplace.Entry, error) {
	return e, nil
}

func (stubMarketplaceRepo) UpdateEntry(context.Context, uuid.UUID, string, string, json.RawMessage) (marketplace.Entry, error) {
	return marketplace.Entry{}, nil
}

func (stubMarketplaceRepo) DeleteEntry(context.Context, uuid.UUID) error { return nil }

func (stubMarketplaceRepo) EnabledEntryIDs(context.Context, uuid.UUID) (map[uuid.UUID]marketplace.TenantEntry, error) {
	return map[uuid.UUID]marketplace.TenantEntry{}, nil
}

func (stubMarketplaceRepo) SetEnabled(context.Context, uuid.UUID, uuid.UUID, bool, uuid.UUID) error {
	return nil
}

func routerWithWorkspaceAdminSurfaces(t *testing.T, gate *recordingGate) http.Handler {
	t.Helper()
	cfg := RouterConfig{
		AuthMiddleware:          authMiddlewareAcceptingAnyToken(t),
		FeatureGateAdminHandler: fakeFeatureGateAdmin{},
		MarketplaceHandler:      marketplace.NewHandler(marketplace.NewService(stubMarketplaceRepo{})),
		InternalToken:           "s3cret",
	}
	if gate != nil {
		cfg.WorkspaceAdminGate = gate
	}
	return NewRouter(cfg)
}

const marketplaceEntryID = "3bf18e5c-2ddd-4114-9205-61fb1184acc0"

const enableBody = `{"enabled":true}`

// workspaceAdminRoutes is every route mounted behind WorkspaceAdminGate.
var workspaceAdminRoutes = []struct {
	name   string
	method string
	path   string
	body   string
}{
	{"feature gate list", http.MethodGet, "/api/v1/admin/feature-gates", ""},
	{"feature gate toggle", http.MethodPut, "/api/v1/admin/feature-gates/ENABLE_RAG", enableBody},
	{"marketplace list", http.MethodGet, "/api/v1/admin/marketplace", ""},
	{"marketplace enable", http.MethodPut, "/api/v1/admin/marketplace/" + marketplaceEntryID + "/enable", enableBody},
}

func TestNewRouterGatesWorkspaceAdminSurfaces(t *testing.T) {
	for _, tc := range workspaceAdminRoutes {
		t.Run(tc.name, func(t *testing.T) {
			gate := &recordingGate{}
			router := routerWithWorkspaceAdminSurfaces(t, gate)

			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer any-token")
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)

			if gate.calls != 1 {
				t.Fatalf("%s %s: the workspace admin gate ran %d times, want 1; the mount does not carry it", tc.method, tc.path, gate.calls)
			}
			if rr.Code != http.StatusForbidden {
				t.Fatalf("%s %s: got %d, want the gate 403; body: %q", tc.method, tc.path, rr.Code, rr.Body.String())
			}
			if got := rr.Body.String(); got != "gate-refused" {
				t.Fatalf("%s %s: response came from %q, not the gate", tc.method, tc.path, got)
			}
		})
	}
}

// A nil gate skips these routes rather than falling back to a wider one. The
// fallback would be RoleSvc.RequirePlatformAdmin, the shape this change moved
// away from; leaving the routes unmounted fails closed instead.
func TestNewRouterSkipsWorkspaceAdminSurfacesWithoutGate(t *testing.T) {
	router := routerWithWorkspaceAdminSurfaces(t, nil)

	for _, tc := range workspaceAdminRoutes {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer any-token")
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s %s with no gate configured: got %d, want 404; body: %q", tc.method, tc.path, rr.Code, rr.Body.String())
		}
	}
}
