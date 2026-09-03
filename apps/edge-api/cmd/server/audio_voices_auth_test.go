package main

import (
	"encoding/json"
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
// keep their authentication. So does a non-GET to the roster path itself: the
// exemption names one method, so an uncredentialed non-GET is refused at the
// JWT path before the handler's own 405 ever applies.
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
		{http.MethodHead, audioVoicesPath},
		{http.MethodGet, audioVoicesPath + "/"},
		{http.MethodGet, audioVoicesPath + "x"},
		{http.MethodGet, "/v1/audio"},
		{http.MethodGet, "/v1/audio/"},
		{http.MethodPost, "/v1/tools/web_search"},
		{http.MethodPost, "/v1/tools/web_fetch"},
		{http.MethodGet, "/v1/models"},
		{http.MethodPost, "/v1/chat/completions"},
		// The shapes an exemption written with HasPrefix or with a cleaned
		// path would let through. The comparison is equality on the decoded
		// r.URL.Path, so each of these is a different string and stays gated;
		// naming them here is what makes a later rewrite to prefix matching
		// turn this red instead of silently opening a paid route.
		{http.MethodGet, "/v1/audio/voices/../speech"},
		{http.MethodGet, "/v1//audio/voices"},
		{http.MethodGet, "/v1/audio/Voices"},
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

// TestVoiceRosterServesTheRealRosterWithNoCredential joins the two halves that
// each looked correct on their own while the route was unreachable.
//
// The exemption test above proves the middleware lets the request past. The
// route-matrix tests prove the path is registered. Neither notices if the
// handler behind it later grows an authorizer, or if the exemption and the
// registration stop naming the same path, and #1377 is exactly what that gap
// looks like from outside: a route registered "without the authorizer", a
// middleware that 401s it anyway, and no test anywhere that put the two
// together and read the body.
//
// So this one drives the real registerAudioVoicesRoute onto a real ServeMux,
// wraps it in the real authSelectorMiddleware, sends the request Open WebUI
// sends, and asserts a roster comes back.
func TestVoiceRosterServesTheRealRosterWithNoCredential(t *testing.T) {
	t.Setenv("OWUI_SHIM_KEY", "")

	mux := http.NewServeMux()
	registerAudioVoicesRoute(mux)

	jwtMW := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
	}

	req := httptest.NewRequest(http.MethodGet, audioVoicesPath, nil)
	rr := httptest.NewRecorder()
	authSelectorMiddleware(jwtMW, mux).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s with no credential answered %d, want 200: %s", audioVoicesPath, rr.Code, rr.Body.String())
	}

	var body struct {
		Voices []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"voices"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("voice roster is not the JSON the dropdown parses: %v (body %s)", err, rr.Body.String())
	}
	if len(body.Voices) == 0 {
		t.Fatalf("voice roster came back empty, which sends the dropdown to its hardcoded fallback (issue #996): %s", rr.Body.String())
	}
	for _, v := range body.Voices {
		if v.ID == "" {
			t.Errorf("a voice came back with no id, which the dropdown cannot select: %s", rr.Body.String())
		}
	}
}
