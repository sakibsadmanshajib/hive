package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/budgets"
)

// The Phase 14 workspace budget surface (PUT/DELETE /api/v1/budgets/{ws} and
// POST/PATCH/DELETE /api/v1/spend-alerts/{ws}) is implemented inside
// budgets.Handler.ServeHTTP, but for its entire life it was never mounted on
// this router. Every request fell through to the /api/v1/ catch-all and came
// back 404, so a tenant owner could not save a hard spend cap or create a
// spend alert from the console.
//
// The handler's own tests in internal/budgets exercise these paths by calling
// budgets.Handler directly with httptest.NewRequest, so they stayed green the
// whole time: the bug lives in this package's wiring, not in the handler.
// Same failure mode, same lesson as router_providers_test.go.
//
// budgets.NewService leaves workspaceCtx nil, which makes the workspace
// branches answer 503 with a message only budgets.Handler emits. That is the
// cheapest proof that dispatch reached the handler rather than the router's
// default 404, and it cannot pass vacuously: an unmounted route returns 404
// with no such body.
func routerWithBudgets(t *testing.T) http.Handler {
	t.Helper()
	return NewRouter(RouterConfig{
		AuthMiddleware: authMiddlewareAcceptingAnyToken(t),
		BudgetsHandler: budgets.NewHandler(budgets.NewService(nil, nil), nil),
	})
}

func TestNewRouterMountsWorkspaceBudgetSurface(t *testing.T) {
	const workspaceID = "d18c9024-d690-4ce9-ab7a-303a3bf97a2f"

	cases := []struct {
		name       string
		method     string
		path       string
		wantInBody string
	}{
		{
			name:       "put workspace budget",
			method:     http.MethodPut,
			path:       "/api/v1/budgets/" + workspaceID,
			wantInBody: "workspace budget surface unavailable",
		},
		{
			name:       "delete workspace budget",
			method:     http.MethodDelete,
			path:       "/api/v1/budgets/" + workspaceID,
			wantInBody: "workspace budget surface unavailable",
		},
		{
			name:       "post spend alert",
			method:     http.MethodPost,
			path:       "/api/v1/spend-alerts/" + workspaceID,
			wantInBody: "spend alert surface unavailable",
		},
		{
			name:       "patch spend alert",
			method:     http.MethodPatch,
			path:       "/api/v1/spend-alerts/" + workspaceID + "/9f1c2b7e-3a4d-4c5e-8f60-1a2b3c4d5e6f",
			wantInBody: "spend alert surface unavailable",
		},
	}

	router := routerWithBudgets(t)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
			req.Header.Set("Authorization", "Bearer any-token")
			rr := httptest.NewRecorder()

			router.ServeHTTP(rr, req)

			if rr.Code == http.StatusNotFound {
				t.Fatalf("%s %s = 404, want the budgets handler to be reached; the route is not mounted on NewRouter",
					tc.method, tc.path)
			}
			if !strings.Contains(rr.Body.String(), tc.wantInBody) {
				t.Fatalf("%s %s = %d %q, want a body containing %q from budgets.Handler",
					tc.method, tc.path, rr.Code, rr.Body.String(), tc.wantInBody)
			}
		})
	}
}
