package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
)

// The admin provider surface was mounted with the providers handler's
// InternalMux(), whose ServeMux patterns are absolute /internal/providers paths,
// so every /api/v1/admin/providers request fell through to the default 404.
// Testing the handler's AdminMux() in isolation does not catch that: the bug
// lives in this package's wiring, not in the handler. Reverting the one-line
// mount here still passed the handler's own tests, so this asserts dispatch
// through NewRouter itself.

// fakeProvidersRouter returns handlers that name which surface was reached.
type fakeProvidersRouter struct{}

func (fakeProvidersRouter) InternalMux() http.Handler { return namedHandler("internal-mux") }
func (fakeProvidersRouter) AdminMux() http.Handler    { return namedHandler("admin-mux") }

func namedHandler(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(name))
	})
}

// passThroughRoleSvc stands in for platform.RoleService. The platform-admin
// decision is covered by that package's own tests; this one is about routing, so
// the gate is a pass-through and every request here counts as an admin.
type passThroughRoleSvc struct{}

func (passThroughRoleSvc) RequirePlatformAdmin(next http.Handler) http.Handler { return next }

// authMiddlewareAcceptingAnyToken points a real auth.Middleware at a stub GoTrue
// so Require admits the request without a network call, keeping the real
// middleware in the chain rather than stubbing it out.
func authMiddlewareAcceptingAnyToken(t *testing.T) *auth.Middleware {
	t.Helper()
	gotrue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/v1/user" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "3f1d5f9c-0b1e-4f0a-9c3d-8a2b7c6d5e4f",
			"email": "admin@example.invalid",
			"email_confirmed_at": "2026-01-01T00:00:00Z",
			"user_metadata": {"selected_tenant_id": "7c9e6679-7425-40de-944b-e07fc1f90ae7"}
		}`))
	}))
	t.Cleanup(gotrue.Close)
	return auth.NewMiddleware(auth.NewClient(gotrue.URL, "anon-key"))
}

func routerWithProviders(t *testing.T) http.Handler {
	t.Helper()
	return NewRouter(RouterConfig{
		AuthMiddleware:  authMiddlewareAcceptingAnyToken(t),
		ProvidersRouter: fakeProvidersRouter{},
		RoleSvc:         passThroughRoleSvc{},
		InternalToken:   "s3cret",
	})
}

func TestNewRouterDispatchesAdminProvidersToAdminMux(t *testing.T) {
	router := routerWithProviders(t)

	for _, path := range []string{
		"/api/v1/admin/providers",
		"/api/v1/admin/providers/3bf18e5c-2ddd-4114-9205-61fb1184acc0",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer any-token")
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s: got %d, want 200; body: %q", path, rr.Code, rr.Body.String())
		}
		if got := rr.Body.String(); got != "admin-mux" {
			t.Fatalf("GET %s: reached %q, want the admin mux", path, got)
		}
	}
}

func TestNewRouterDispatchesInternalProvidersToInternalMux(t *testing.T) {
	router := routerWithProviders(t)

	req := httptest.NewRequest(http.MethodGet, "/internal/providers", nil)
	req.Header.Set(InternalTokenHeader, "s3cret")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if got := rr.Body.String(); got != "internal-mux" {
		t.Fatalf("GET /internal/providers: reached %q, want the internal mux", got)
	}
}

func TestNewRouterAdminProvidersRequiresAuth(t *testing.T) {
	router := routerWithProviders(t)

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/admin/providers", nil))

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated admin request: got %d, want 401", rr.Code)
	}
	if rr.Body.String() == "admin-mux" {
		t.Fatal("unauthenticated request reached the admin mux")
	}
}
