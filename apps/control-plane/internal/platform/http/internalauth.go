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
// When token is empty the middleware is a pass-through: this preserves
// local/dev and test ergonomics, and the control-plane logs a startup warning
// so an unauthenticated deployment is visible. When token is set, the request
// must carry a matching X-Internal-Token header (compared in constant time);
// otherwise it is rejected with 401 before reaching the handler.
func RequireInternalToken(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	expected := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := []byte(r.Header.Get(InternalTokenHeader))
		if subtle.ConstantTimeCompare(provided, expected) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
