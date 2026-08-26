package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/matrix"
)

// httpMux is the subset of *http.ServeMux's API that route registration
// helpers below use. Accepting this instead of the concrete type lets main
// swap in routeRecorder and have every registration recorded, at zero cost
// to any existing call site: *http.ServeMux already satisfies it, so
// production code and the existing tests that build a plain http.NewServeMux()
// (gated_routes_test.go) keep compiling unchanged.
type httpMux interface {
	Handle(pattern string, handler http.Handler)
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// routeRecorder wraps a real *http.ServeMux, recording every pattern
// registered through it. main() serves through it exactly like a plain mux;
// assertMatrixCoverage then reads Patterns() back, so the source of truth
// for "what routes does edge-api actually serve" is the registration code
// itself, never a parallel hand-kept list.
//
// Buglog entry matrix-missing-proprietary-endpoints (2026-07-17) fixed a
// whole family of proprietary routes missing from support-matrix.json with
// hand-added matrix entries plus a hand-listed regression test
// (unsupported_integration_test.go). The same defect shipped again for
// GET /v1/audio/voices (#1079) and the /v1/agent/schedules family (#1081):
// the hand list only ever covered the routes someone remembered to add to
// it. routeRecorder exists so a new mux.Handle/HandleFunc call on the main
// server mux can no longer ship with zero matrix coverage: main() asserts
// against what this recorder actually saw, not against anyone's memory of
// the route set.
type routeRecorder struct {
	*http.ServeMux
	patterns []string
}

func newRouteRecorder() *routeRecorder {
	return &routeRecorder{ServeMux: http.NewServeMux()}
}

func (r *routeRecorder) Handle(pattern string, handler http.Handler) {
	r.patterns = append(r.patterns, pattern)
	r.ServeMux.Handle(pattern, handler)
}

func (r *routeRecorder) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	r.patterns = append(r.patterns, pattern)
	r.ServeMux.HandleFunc(pattern, handler)
}

// Patterns returns every pattern registered through r so far.
func (r *routeRecorder) Patterns() []string {
	return append([]string(nil), r.patterns...)
}

// assertMatrixCoverage returns an error naming every /v1/-prefixed pattern
// in patterns that m has no entry for at all (see SupportMatrix.HasCoverage).
// Non-/v1/ patterns are skipped: UnsupportedEndpointMiddleware only ever
// checks /v1/ paths, so the matrix has no opinion on anything else.
//
// This catches a route family shipping with zero matrix awareness, the
// shape that has recurred twice. It cannot catch a single new suffix added
// inside a handler's own internal path dispatch under an already-covered
// prefix (see HasCoverage's doc comment); that still needs a human to add
// the matrix entry by hand.
func assertMatrixCoverage(patterns []string, m *matrix.SupportMatrix) error {
	var missing []string
	for _, p := range patterns {
		if !strings.HasPrefix(p, "/v1/") {
			continue
		}
		if !m.HasCoverage(p) {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("support-matrix.json has no entry for %d registered route(s), edge-api refuses to start: %s",
		len(missing), strings.Join(missing, ", "))
}
