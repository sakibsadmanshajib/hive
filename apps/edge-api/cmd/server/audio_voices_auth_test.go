package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// exerciseAuthSelector drives the real authSelectorMiddleware construction,
// the same one main() performs, with a JWT stand-in that always 401s.
//
// One helper rather than a closure pair rebuilt at every call site: the table
// below and webtools_list_auth_test.go were each carrying their own copy, and
// several copies of a middleware harness is several places for the exemption's
// meaning to drift.
//
// The JWT stand-in is what makes the result readable. Reaching the mux can only
// happen through an exemption, because every other path is answered by the
// stand-in before the mux is consulted, so reachedMux is a direct statement
// about the exemption rather than about any handler behind it.
func exerciseAuthSelector(method, path string) (jwtInvoked, reachedMux bool) {
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
	req := httptest.NewRequest(method, path, nil)
	authSelectorMiddleware(jwtMW, next).ServeHTTP(httptest.NewRecorder(), req)
	return jwtInvoked, reachedMux
}

// TestAuthSelectorExemptions is the whole exemption set, in one table.
//
// Two routes under /v1/ carry no principal, and both are registered with no
// authorizer on purpose. GET /v1/tools is the descriptor list, which the chat
// shim reads with no credential (issue #776, PR #1730). GET /v1/audio/voices is
// the voice roster, which Open WebUI's voice dropdowns fetch with no
// Authorization header at all; a 401 there sends get_available_voices to its
// hardcoded fallback list, which is the shape issue #996 was closed to prevent.
//
// Both registrations are inert without an exemption here. authSelectorMiddleware
// intercepts every path under /v1/, and auth.Selector routes to the API-key
// handler only for a "Bearer hk_..." credential, sending everything else,
// including a request with no Authorization header at all, to the JWT
// middleware, which answers 401 before the mux is ever reached. That is what
// issue #1377 reported, from a plain curl on the box:
//
//	$ curl -s http://localhost:8080/v1/audio/voices
//	{"error":{"code":"UNAUTHENTICATED","message":"missing bearer",...}}
//
// One table for both routes, rather than a completeness claim per route file.
// Two tests that each name themselves the complete exemption set cannot both
// stay true, and the one that goes stale goes stale silently, in the direction
// that still passes.
func TestAuthSelectorExemptions(t *testing.T) {
	t.Setenv("OWUI_SHIM_KEY", "")

	cases := []struct {
		name   string
		method string
		path   string
		exempt bool
	}{
		{"the voice roster, unauthenticated", http.MethodGet, audioVoicesPath, true},
		{"the descriptor list, unauthenticated", http.MethodGet, webToolsListPath, true},

		// The decoded-path semantics, pinned rather than assumed. r.URL.Path is
		// decoded before this comparison, so these spell the exempt path and
		// are exempt. Asserting them as exempt documents what the code actually
		// does; asserting them as gated would claim a stricter rule than the
		// middleware implements and would go red against correct code. If the
		// exemption is ever made strict about the raw spelling, this is where
		// that change shows up.
		{"percent-encoded spelling of the roster", http.MethodGet, "/v1/%61udio/voices", true},
		{"percent-encoded final character", http.MethodGet, "/v1/audio/voice%73", true},
		// Exempt at the middleware and a 404 at the mux, because an encoded
		// separator matches no registered pattern. Named so that the divergence
		// is recorded rather than discovered.
		{"encoded separator", http.MethodGet, "/v1/audio%2fvoices", true},

		// The neighbours that spend credits. All three audio routes sit one
		// path segment from the roster and all three keep their authentication.
		{"speech", http.MethodPost, "/v1/audio/speech", false},
		{"transcriptions", http.MethodPost, "/v1/audio/transcriptions", false},
		{"translations", http.MethodPost, "/v1/audio/translations", false},
		{"web search", http.MethodPost, "/v1/tools/web_search", false},
		{"web fetch", http.MethodPost, "/v1/tools/web_fetch", false},
		{"models", http.MethodGet, "/v1/models", false},
		{"chat completions", http.MethodPost, "/v1/chat/completions", false},

		// The exemption names one method. An uncredentialed non-GET is refused
		// at the JWT path, before the handler's own 405, which answers a
		// credentialed caller.
		{"POST to the roster", http.MethodPost, audioVoicesPath, false},
		{"DELETE the roster", http.MethodDelete, audioVoicesPath, false},
		{"HEAD the roster", http.MethodHead, audioVoicesPath, false},
		{"POST to the descriptor list", http.MethodPost, webToolsListPath, false},
		{"DELETE the descriptor list", http.MethodDelete, webToolsListPath, false},

		// The shapes an exemption written with a prefix match or a cleaned path
		// would let through. Each is a different string from the constant, so
		// each stays gated; naming them is what turns a later rewrite to prefix
		// matching red instead of letting it quietly open a paid route.
		{"traversal onto speech", http.MethodGet, "/v1/audio/voices/../speech", false},
		{"trailing slash on the roster", http.MethodGet, audioVoicesPath + "/", false},
		{"suffixed roster", http.MethodGet, audioVoicesPath + "x", false},
		{"doubled slash", http.MethodGet, "/v1//audio/voices", false},
		{"case variant", http.MethodGet, "/v1/audio/Voices", false},
		{"the audio prefix itself", http.MethodGet, "/v1/audio", false},
		{"the audio prefix with a slash", http.MethodGet, "/v1/audio/", false},
		{"trailing slash on the descriptor list", http.MethodGet, webToolsListPath + "/", false},
		{"suffixed descriptor list", http.MethodGet, webToolsListPath + "x", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jwtInvoked, reachedMux := exerciseAuthSelector(tc.method, tc.path)
			if tc.exempt {
				if jwtInvoked {
					t.Errorf("%s %s was sent to the JWT path, where a missing bearer is a 401", tc.method, tc.path)
				}
				if !reachedMux {
					t.Errorf("%s %s is meant to be exempt but did not reach the mux", tc.method, tc.path)
				}
				return
			}
			if reachedMux {
				t.Errorf("%s %s reached the mux with no credential; the exemption is wider than it should be", tc.method, tc.path)
			}
			if !jwtInvoked {
				t.Errorf("%s %s with no credential was not sent to the JWT path", tc.method, tc.path)
			}
		})
	}
}

// TestVoiceRosterServesTheRealRosterWithNoCredential joins the two halves that
// each looked correct on their own while the route was unreachable.
//
// The table above proves the middleware lets the request past. The route-matrix
// tests prove the path is registered. Neither notices if the handler behind it
// later grows an authorizer, or if the exemption and the registration stop
// naming the same path, and issue #1377 is exactly the shape of a defect that
// hides between the two. So this drives the real registerAudioVoicesRoute onto
// a real ServeMux, wraps it in the real authSelectorMiddleware, sends the
// request Open WebUI sends, and reads the body.
//
// Reachability and shape only. What the roster actually contains is pinned in
// internal/audio/handler_voices_test.go, which is where a change to the voices
// themselves belongs, and asserting it again here would be a second copy to
// keep in step. So this is deliberately not a guard against the wrong roster: a
// hardcoded fallback list would satisfy it. It guards against no roster.
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
			ID string `json:"id"`
		} `json:"voices"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("voice roster is not the JSON the dropdown parses: %v (body %s)", err, rr.Body.String())
	}
	if len(body.Voices) == 0 {
		t.Fatalf("voice roster came back empty, so the dropdown has nothing to offer: %s", rr.Body.String())
	}
	for _, v := range body.Voices {
		if v.ID == "" {
			t.Errorf("a voice came back with no id, which the dropdown cannot select: %s", rr.Body.String())
		}
	}
}
