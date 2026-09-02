package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDescriptorListIsReachableWithoutACredential pins the one /v1/ route that
// carries no principal.
//
// GET /v1/tools serves a compiled-in constant and the chat shim reads it with
// no Authorization header on purpose (deploy/docker/owui-patches/
// hive_web_tools.py). Everything under /v1/ with no bearer falls through
// auth.Selector to the JWT handler, which answers 401 on a missing bearer
// before any mux entry runs, so without the exemption in
// authSelectorMiddleware the shim read 401s on every turn, advertises nothing,
// and prefer_legacy puts every turn back on the legacy path. The feature ships
// merged and inert, which is the shape of issue #776.
//
// This drives the real authSelectorMiddleware construction, the same one
// main() performs, with a jwtMW stand-in that always 401s, so reaching the mux
// can only happen through the exemption.
func TestDescriptorListIsReachableWithoutACredential(t *testing.T) {
	t.Setenv("OWUI_SHIM_KEY", "")

	var jwtInvoked, reachedMux bool
	jwtMW := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			jwtInvoked = true
			w.WriteHeader(http.StatusUnauthorized)
		})
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reachedMux = true
		w.WriteHeader(http.StatusOK)
	})

	handler := authSelectorMiddleware(jwtMW, next)

	req := httptest.NewRequest(http.MethodGet, webToolsListPath, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if jwtInvoked {
		t.Errorf("the descriptor list must not be sent to the JWT path, where a missing bearer is a 401")
	}
	if !reachedMux {
		t.Fatalf("GET %s with no credential did not reach the mux: status %d", webToolsListPath, rr.Code)
	}
}

// TestOnlyTheDescriptorListIsExemptFromAuth is the other half. The exemption
// is one path and one method; the two routes that spend credits, a non-GET to
// the list path, and every neighbouring path keep their authentication.
func TestOnlyTheDescriptorListIsExemptFromAuth(t *testing.T) {
	t.Setenv("OWUI_SHIM_KEY", "")

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/tools/web_search"},
		{http.MethodPost, "/v1/tools/web_fetch"},
		{http.MethodPost, webToolsListPath},
		{http.MethodDelete, webToolsListPath},
		{http.MethodGet, webToolsListPath + "/"},
		{http.MethodGet, webToolsListPath + "x"},
		{http.MethodGet, "/v1/models"},
	}

	for _, tc := range cases {
		var jwtInvoked, reachedMux bool
		jwtMW := func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				jwtInvoked = true
				w.WriteHeader(http.StatusUnauthorized)
			})
		}
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reachedMux = true
			w.WriteHeader(http.StatusOK)
		})

		handler := authSelectorMiddleware(jwtMW, next)
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if !jwtInvoked || reachedMux {
			t.Errorf("%s %s reached the mux with no credential: it must stay authenticated (jwtInvoked=%v reachedMux=%v)",
				tc.method, tc.path, jwtInvoked, reachedMux)
		}
	}
}
