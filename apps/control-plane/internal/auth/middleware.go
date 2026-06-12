package auth

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Middleware wraps an http.Handler requiring a valid Supabase bearer token on
// all requests. The resolved Viewer is stored in the request context.
type Middleware struct {
	client *Client
}

// NewMiddleware returns an auth middleware using the given Supabase client.
func NewMiddleware(client *Client) *Middleware {
	return &Middleware{client: client}
}

// OptionalRequire wraps handler h with best-effort auth. If a bearer token is
// present and valid, the resolved Viewer (including TenantID from
// raw_user_meta_data.selected_tenant_id) is stored in context via WithViewer.
// Missing or invalid tokens are silently ignored; the request proceeds
// unauthenticated. Use ViewerFromContext to read the result.
func (m *Middleware) OptionalRequire(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token := extractBearer(r); token != "" {
			if viewer, err := m.client.LookupUser(r.Context(), token); err == nil {
				r = r.WithContext(WithViewer(r.Context(), viewer))
			}
		}
		h.ServeHTTP(w, r)
	})
}

// Require wraps handler h, returning 401 when the bearer token is missing or invalid.
func (m *Middleware) Require(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractBearer(r)
		if token == "" {
			writeUnauthorized(w, "missing authorization header")
			return
		}

		viewer, err := m.client.LookupUser(r.Context(), token)
		if err != nil {
			writeUnauthorized(w, "invalid or expired token")
			return
		}

		r = r.WithContext(WithViewer(r.Context(), viewer))
		h.ServeHTTP(w, r)
	})
}

func extractBearer(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return parts[1]
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeUnauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: msg})
}
