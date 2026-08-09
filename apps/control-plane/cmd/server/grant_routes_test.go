package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/authz"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// Issue #793. The platform-admin gate on /v1/admin/credit-grants is the control
// that keeps a non-admin from minting spendable balance. It could be deleted
// outright and `go test ./apps/control-plane/... -count=1 -short` still exited 0
// across all 52 packages, because every test that touches this code builds its
// own middleware or calls the handler directly. The attachment itself, gate on
// route, had no test at all.
//
// This drives the registration helper main() calls, through a real
// http.ServeMux, and asserts the outcome per (path, actor) pair. Deleting the
// gate, widening it to a non-platform permission, or accidentally putting it on
// the self-service read surface each turns this red.

const (
	adminPath      = "/v1/admin/credit-grants"
	adminChildPath = "/v1/admin/credit-grants/00000000-0000-0000-0000-000000000001"
	selfPath       = "/v1/credit-grants/me"
)

// viewer models the caller's identity as the process would have resolved it.
type viewer struct {
	authenticated bool
	actor         authz.Actor
}

var (
	// A verified workspace owner: the most privileged non-platform-admin
	// identity in the model, and exactly the caller who must not be able to
	// mint credit.
	workspaceOwner = viewer{
		authenticated: true,
		actor:         authz.Actor{Role: platform.RoleOwner, Verified: true, IsAdmin: false},
	}
	platformAdmin = viewer{
		authenticated: true,
		actor:         authz.Actor{Role: platform.RoleOwner, Verified: true, IsAdmin: true},
	}
	anonymous = viewer{authenticated: false}
)

// stubAuth stands in for auth.Middleware.Require: 401 without a viewer, pass
// through with one. The real middleware validates a Supabase bearer token and
// is covered in the auth package; what is under test here is the authz gate
// sitting behind it and the paths both are attached to.
func stubAuth(v viewer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !v.authenticated {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing authorization header"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func TestCreditGrantRouteAttachment(t *testing.T) {
	cases := []struct {
		name        string
		caller      viewer
		method      string
		path        string
		wantStatus  int
		wantReached bool
	}{
		{
			// The financial control. A verified owner of a workspace holds
			// every workspace permission and still must not reach the grant
			// minting surface.
			name:        "workspace owner is denied the admin create surface",
			caller:      workspaceOwner,
			method:      http.MethodPost,
			path:        adminPath,
			wantStatus:  http.StatusForbidden,
			wantReached: false,
		},
		{
			name:        "workspace owner is denied the admin list surface",
			caller:      workspaceOwner,
			method:      http.MethodGet,
			path:        adminPath,
			wantStatus:  http.StatusForbidden,
			wantReached: false,
		},
		{
			// Registered by its own mux.Handle call, so it can lose the gate
			// independently of the exact path above.
			name:        "workspace owner is denied a single admin grant read",
			caller:      workspaceOwner,
			method:      http.MethodGet,
			path:        adminChildPath,
			wantStatus:  http.StatusForbidden,
			wantReached: false,
		},
		{
			// Positive control. Without it a gate that denied everyone, or a
			// route that had stopped existing, would still satisfy the denial
			// cases above.
			name:        "platform admin reaches the admin surface",
			caller:      platformAdmin,
			method:      http.MethodPost,
			path:        adminPath,
			wantStatus:  http.StatusOK,
			wantReached: true,
		},
		{
			name:        "platform admin reaches a single admin grant read",
			caller:      platformAdmin,
			method:      http.MethodGet,
			path:        adminChildPath,
			wantStatus:  http.StatusOK,
			wantReached: true,
		},
		{
			name:        "anonymous caller is refused before the admin gate",
			caller:      anonymous,
			method:      http.MethodPost,
			path:        adminPath,
			wantStatus:  http.StatusUnauthorized,
			wantReached: false,
		},
		{
			// The self surface is read-only and deliberately not admin-gated.
			// Wrapping it in the admin gate by mistake would lock every
			// ordinary user out of their own grant history.
			name:        "workspace owner reads their own grants",
			caller:      workspaceOwner,
			method:      http.MethodGet,
			path:        selfPath,
			wantStatus:  http.StatusOK,
			wantReached: true,
		},
		{
			name:        "anonymous caller is refused the self surface",
			caller:      anonymous,
			method:      http.MethodGet,
			path:        selfPath,
			wantStatus:  http.StatusUnauthorized,
			wantReached: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var adminReached, selfReached bool
			adminSpy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				adminReached = true
				w.WriteHeader(http.StatusOK)
			})
			selfSpy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				selfReached = true
				w.WriteHeader(http.StatusOK)
			})

			authzMW := authz.NewMiddleware(func(r *http.Request) (authz.Actor, error) {
				return tc.caller.actor, nil
			})

			mux := http.NewServeMux()
			registerCreditGrantRoutes(mux, stubAuth(tc.caller), authzMW, adminSpy, selfSpy)

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), tc.method, tc.path, nil))

			if rec.Code != tc.wantStatus {
				t.Errorf("%s %s: got status %d, want %d (body %s)",
					tc.method, tc.path, rec.Code, tc.wantStatus, rec.Body.String())
			}
			reached := adminReached || selfReached
			if reached != tc.wantReached {
				t.Errorf("%s %s: handler reached = %v, want %v",
					tc.method, tc.path, reached, tc.wantReached)
			}
			// A request to an admin path must never be served by the
			// ungated self handler, and the reverse.
			if tc.path == selfPath && adminReached {
				t.Errorf("%s was served by the admin handler", selfPath)
			}
			if tc.path != selfPath && selfReached {
				t.Errorf("%s was served by the self handler", tc.path)
			}
		})
	}
}
