package auth

import (
	"net/http"
	"strings"
)

// apiKeyTokenPrefix is the canonical Hive API-key token prefix. The
// auth scheme word ("Bearer") is matched case-insensitively per RFC
// 7235 §2.1; the token body itself stays case-sensitive because the
// random suffix is base62 and an upper/lower swap would identify a
// different key.
const apiKeyTokenPrefix = "hk_"

// Selector routes a request to the API-key path when the Authorization
// header carries a "Bearer hk_..." credential (scheme matched
// case-insensitively, token case-sensitive) and to the JWT path
// otherwise.
//
// The selector deliberately does not parse or validate the credential
// itself; it only chooses which downstream handler runs. Each handler
// remains responsible for its own validation, error shape, and for
// setting the authenticated principal on the request context via
// WithUser before invoking inner handlers.
func Selector(jwtHandler, apiKeyHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		// Split into <scheme> <token>. Single space matches the wire
		// format Supabase, Stripe, OpenRouter, and curl all emit; any
		// header that lacks a space (or has no token body) falls
		// through to the JWT handler which will reject it.
		scheme, rest, ok := strings.Cut(h, " ")
		// Defence-in-depth: a multi-valued Authorization header (RFC 7230
		// allows commas) or a token with embedded whitespace must never
		// route to the API-key path. Such a value cannot be a valid hk_
		// token and is the canonical shape of a credential-smuggling
		// attempt. Send it to the JWT handler, which will 401.
		if ok && strings.EqualFold(scheme, "Bearer") &&
			strings.HasPrefix(rest, apiKeyTokenPrefix) &&
			!strings.ContainsAny(rest, ", \t") {
			apiKeyHandler.ServeHTTP(w, r)
			return
		}
		jwtHandler.ServeHTTP(w, r)
	})
}
