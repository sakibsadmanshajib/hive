package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestVoiceRosterIsReachableWithoutACredential pins the second /v1/ route that
// carries no principal (issue #1377).
//
// registerAudioVoicesRoute attaches audio.VoicesHandler() straight onto the
// mux with no authorizer and no tenant voice gate, deliberately: Open WebUI's
// voice dropdowns fetch this with no Authorization header at all, and gating
// it silently reinstates the hardcoded alloy-style fallback list that issue
// #996 exists to prevent.
//
// It was gated anyway, one layer out. authSelectorMiddleware intercepts every
// path under /v1/, and auth.Selector sends anything that is not a "Bearer
// hk_..." request, a request with no Authorization header included, to the JWT
// middleware, which answers 401 before the mux is ever reached. So the
// unauthenticated registration could not take effect while JWT auth was
// configured, which it is on the box:
//
//	$ curl -s http://localhost:8080/v1/audio/voices
//	{"error":{"code":"UNAUTHENTICATED","message":"missing bearer",...}}
//
// This drives the real authSelectorMiddleware construction, the same one
// main() performs, with a jwtMW stand-in that always 401s, so reaching the mux
// can only happen through the exemption.
func TestVoiceRosterIsReachableWithoutACredential(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, audioVoicesPath, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if jwtInvoked {
		t.Errorf("the voice roster must not be sent to the JWT path, where a missing bearer is a 401")
	}
	if !reachedMux {
		t.Fatalf("GET %s with no credential did not reach the mux: status %d", audioVoicesPath, rr.Code)
	}
}

// TestOnlyTheTwoPublicReadsAreExemptFromAuth is the other half, and the half
// issue #1377 asks for by name: proof that no other /v1/ path gained an
// exemption alongside the voice roster.
//
// The neighbouring audio routes are the ones that matter. All three spend
// credits, all three sit one path segment away from the roster, and all three
// keep their authentication. So does a non-GET to the roster path itself,
// which the handler answers 405 for on its own.
func TestOnlyTheTwoPublicReadsAreExemptFromAuth(t *testing.T) {
	t.Setenv("OWUI_SHIM_KEY", "")

	exempt := []struct {
		method string
		path   string
	}{
		{http.MethodGet, audioVoicesPath},
		{http.MethodGet, webToolsListPath},
	}

	gated := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/audio/speech"},
		{http.MethodPost, "/v1/audio/transcriptions"},
		{http.MethodPost, "/v1/audio/translations"},
		{http.MethodPost, audioVoicesPath},
		{http.MethodDelete, audioVoicesPath},
		{http.MethodGet, audioVoicesPath + "/"},
		{http.MethodGet, audioVoicesPath + "x"},
		{http.MethodGet, "/v1/audio"},
		{http.MethodGet, "/v1/audio/"},
		{http.MethodPost, "/v1/tools/web_search"},
		{http.MethodPost, "/v1/tools/web_fetch"},
		{http.MethodGet, "/v1/models"},
	}

	for _, tc := range exempt {
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
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rr := httptest.NewRecorder()
		authSelectorMiddleware(jwtMW, next).ServeHTTP(rr, req)

		if jwtInvoked || !reachedMux {
			t.Errorf("%s %s is meant to be exempt but did not reach the mux (jwt invoked: %v)", tc.method, tc.path, jwtInvoked)
		}
	}

	for _, tc := range gated {
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
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rr := httptest.NewRecorder()
		authSelectorMiddleware(jwtMW, next).ServeHTTP(rr, req)

		if reachedMux {
			t.Errorf("%s %s reached the mux with no credential; the exemption is wider than it should be", tc.method, tc.path)
		}
		if !jwtInvoked {
			t.Errorf("%s %s with no credential was not sent to the JWT path", tc.method, tc.path)
		}
	}
}
