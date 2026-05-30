// Package cpauth centralizes the shared-secret header that edge-api attaches to
// every control-plane /internal/* request, so the control-plane can
// authenticate this service (issue #108).
package cpauth

import (
	"net/http"
	"os"
)

// Header is the request header carrying the shared secret. It must match the
// control-plane's expected header (platform/http.InternalTokenHeader).
const Header = "X-Internal-Token"

// token is read once at process start from CONTROL_PLANE_INTERNAL_TOKEN. When
// empty, SetHeader is a no-op (local/dev), matching the control-plane's
// unauthenticated-when-unset behaviour.
var token = os.Getenv("CONTROL_PLANE_INTERNAL_TOKEN")

// SetHeader attaches the internal shared-secret header to req when a token is
// configured. Safe to call on every outbound control-plane /internal/* request.
func SetHeader(req *http.Request) {
	if req == nil || token == "" {
		return
	}
	req.Header.Set(Header, token)
}

// SetTokenForTest overrides the configured token. Test-only.
func SetTokenForTest(t string) { token = t }
