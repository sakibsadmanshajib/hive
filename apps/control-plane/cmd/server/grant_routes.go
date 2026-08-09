package main

import (
	"net/http"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/authz"
)

// registerCreditGrantRoutes attaches the credit-grant surfaces to mux.
//
// Credit grants mint spendable balance, so the platform-admin gate on the
// /v1/admin/credit-grants paths is a financial control, not a convenience. It
// used to be applied inline in main(). Deleting it left the whole control-plane
// suite green across every package, because the grants package tests call the
// handler directly and the authz package tests build their own middleware and
// call that directly: nothing ever asked whether the gate was on the route.
// Issue #793.
//
// requireAuth is the authentication wrapper (auth.Middleware.Require in the
// process, a viewer-injecting stub in the test) and stays outermost, matching
// the order the process serves. authzMW carries the real policy.
func registerCreditGrantRoutes(
	mux *http.ServeMux,
	requireAuth func(http.Handler) http.Handler,
	authzMW authz.Middleware,
	adminHandler http.Handler,
	selfHandler http.Handler,
) {
	protectedAdmin := requireAuth(authzMW.RequirePermission(authz.PermPlatformAdmin)(adminHandler))
	mux.Handle("/v1/admin/credit-grants", protectedAdmin)
	mux.Handle("/v1/admin/credit-grants/", protectedAdmin)

	// Read-only self surface: authenticated, deliberately not admin-gated.
	mux.Handle("/v1/credit-grants/me", requireAuth(selfHandler))
}
