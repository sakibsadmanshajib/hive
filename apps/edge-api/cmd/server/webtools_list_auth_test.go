package main

import (
	"net/http"
	"testing"
)

// TestDescriptorListIsReachableWithoutACredential pins the descriptor list,
// one of the two /v1/ routes that carry no principal. The other is the voice
// roster (issue #1377); TestAuthSelectorExemptions in audio_voices_auth_test.go
// carries both, and every negative case for both.
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

	jwtInvoked, reachedMux := exerciseAuthSelector(http.MethodGet, webToolsListPath)

	if jwtInvoked {
		t.Errorf("the descriptor list must not be sent to the JWT path, where a missing bearer is a 401")
	}
	if !reachedMux {
		t.Fatalf("GET %s with no credential did not reach the mux", webToolsListPath)
	}
}

// The other half, "and nothing else is exempt", used to live here as
// TestOnlyTheDescriptorListIsExemptFromAuth. It moved to
// TestAuthSelectorExemptions in audio_voices_auth_test.go when issue #1377
// added the voice roster as a second exempt route, and it took the web tools
// negative cases with it.
//
// It moved rather than gaining a neighbour because the claim is about the
// middleware, not about either route: a test named for one route that asserts
// the complete exemption set becomes quietly false the moment a second route
// is exempted, and false in the direction that still passes. One table now
// carries both routes and every negative case for both.
