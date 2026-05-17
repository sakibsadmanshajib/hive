// Package auth — JWT request middleware.
//
// jwtMiddleware validates the bearer token using SupabaseJWTValidator, sets
// the authenticated principal on the request context via WithUser, and
// emits OpenAI-shaped UNAUTHORIZED errors for missing or invalid tokens.
// Audit hooks are invoked on validation failures so the call site (main.go)
// can attribute auth failures to an audit log without leaking provider
// names or token contents into the response.
package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// AuditFailFunc is invoked by jwtMiddleware on validation failures. It is
// kept as a function value (not an interface) so callers can adapt their
// existing audit.Logger without introducing a new dependency on this
// package.
type AuditFailFunc func(action, reason, ip string)

// JWTMiddleware validates the bearer token, populates the request context
// with the authenticated principal, and audits failures. Successful
// validations forward to `next` with the principal attached.
//
// A nil validator is treated as a fatal misconfiguration: rather than
// panicking on first request (or — worse — silently letting every
// request through), the middleware fails closed with 503 so the
// operator notices.
func JWTMiddleware(v *SupabaseJWTValidator, auditFail AuditFailFunc) func(http.Handler) http.Handler {
	if v == nil {
		return func(_ http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if auditFail != nil {
					auditFail("AUTH_JWT_MISCONFIGURED", "nil validator", r.RemoteAddr)
				}
				writeAuthError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service unavailable")
			})
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract the bearer token. RFC 7235 §2.1 makes the scheme
			// word case-insensitive; the selector already routes
			// "bearer hk_*" to the API-key path, so anything that
			// lands here is a JWT credential and we must accept any
			// capitalisation of the scheme word to stay consistent
			// with the selector's classification. Use the same
			// strings.Cut + EqualFold pattern as selector.go.
			scheme, raw, ok := strings.Cut(r.Header.Get("Authorization"), " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") || raw == "" {
				if auditFail != nil {
					auditFail("AUTH_JWT_MISSING", "missing bearer", r.RemoteAddr)
				}
				writeAuthError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "missing bearer")
				return
			}
			claims, err := v.Parse(r.Context(), raw)
			if err != nil {
				code := "UNAUTHENTICATED"
				action := "AUTH_JWT_INVALID"
				if errors.Is(err, ErrJWTExpired) {
					code = "JWT_EXPIRED"
					action = "AUTH_JWT_EXPIRED"
				}
				if auditFail != nil {
					// Never echo err.Error() back to the audit hook —
					// jwx error messages can include token fragments
					// (kid, header slices). Use a fixed short reason.
					auditFail(action, "token validation failed", r.RemoteAddr)
				}
				writeAuthError(w, http.StatusUnauthorized, code, "invalid token")
				return
			}
			// Defence-in-depth: the validator parses missing/malformed
			// claims to zero-value UUIDs (so per-claim mistakes do not
			// surface as parse failures). Reject any token that arrives
			// here without a usable principal so downstream handlers
			// never see a Nil-UUID user.
			if claims.Sub == uuid.Nil || claims.TenantID == uuid.Nil {
				if auditFail != nil {
					auditFail("AUTH_JWT_INVALID", "missing principal claims", r.RemoteAddr)
				}
				writeAuthError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid token")
				return
			}
			ctx := WithUser(r.Context(), &User{
				ID:       claims.Sub,
				TenantID: claims.TenantID,
				Role:     claims.Role,
				Email:    claims.Email,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// writeAuthError emits an OpenAI-shaped JSON error body. We marshal via
// encoding/json so embedded quotes or control characters in code/msg are
// escaped properly; hand-built JSON would let a future caller smuggle
// payload by passing an attacker-influenced msg.
func writeAuthError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": msg,
			"type":    "UNAUTHORIZED",
		},
	})
}
