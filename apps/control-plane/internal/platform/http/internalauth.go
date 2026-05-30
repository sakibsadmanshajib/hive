package http

import (
	"crypto/subtle"
	"net/http"
)

// InternalTokenHeader is the header edge-api sends on every service-to-service
// call to the control-plane's /internal/* endpoints.
const InternalTokenHeader = "X-Internal-Token"

// RequireInternalToken gates the control-plane's internal service-to-service
// endpoints (/internal/*) with a shared secret, closing the issue where any
// network peer could call them (issue #108).
//
// It FAILS CLOSED: the request must carry an X-Internal-Token header matching
// the configured token (compared in constant time), or it is rejected with 401
// before reaching the handler. When token is empty (unconfigured) every request
// is rejected — a misconfigured deployment denies internal traffic rather than
// silently leaving the endpoints open. Local/dev and CI supply a matching token
// via docker-compose so traffic flows; the control-plane also logs a startup
// warning when the token is unset.
func RequireInternalToken(token string, next http.Handler) http.Handler {
	expected := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := []byte(r.Header.Get(InternalTokenHeader))
		// len(expected)==0 means no token is configured: deny unconditionally
		// (ConstantTimeCompare("","") would otherwise report a match).
		if len(expected) == 0 || subtle.ConstantTimeCompare(provided, expected) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
