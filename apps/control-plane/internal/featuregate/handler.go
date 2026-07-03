// Package featuregate exposes an internal service-to-service endpoint that
// returns per-tenant feature flags so edge-api can gate capabilities without
// querying the database directly.
//
// GET /internal/featuregate/{tenant_id}
//
// The tenant_id path segment is the UUID of the requesting tenant. The
// handler resolves flags from the tenant_settings table via the shared
// settings.Resolver (which carries its own short cache). The response is
// a flat JSON object; all unknown/unset flags default to false.
//
// Auth: guarded upstream by RequireInternalToken (all /internal/* routes).
package featuregate

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenant/settings"
)

// FlagsResponse is the JSON body returned to the edge-api.
type FlagsResponse struct {
	RAGEnabled    bool `json:"rag_enabled"`
	VoiceEnabled  bool `json:"voice_enabled"`
	RelayEnabled  bool `json:"relay_enabled"`
	CoworkEnabled bool `json:"cowork_enabled"`
	// SSOEnabled is true when at least one SSO provider key is enabled for the
	// tenant: ENABLE_SSO_SAML, ENABLE_SSO_GOOGLE, or ENABLE_SSO_MICROSOFT.
	// The edge-api gate uses this single flag; provider selection is GoTrue's
	// responsibility, not the gate's.
	SSOEnabled bool `json:"sso_enabled"`
}

// Resolver is the narrow interface the handler needs from settings.Resolver.
type Resolver interface {
	IsEnabled(ctx context.Context, tenantID uuid.UUID, key settings.Key) bool
}

// Handler serves GET /internal/featuregate/{tenant_id}.
type Handler struct {
	resolver Resolver
}

// NewHandler constructs a Handler. resolver must not be nil.
func NewHandler(resolver Resolver) *Handler {
	return &Handler{resolver: resolver}
}

// ServeHTTP handles GET /internal/featuregate/{tenant_id}.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract {tenant_id} from the trailing path segment. The mux is
	// registered with the prefix pattern "/internal/featuregate/" (see
	// router.go), but net/http's ServeMux does not strip that prefix from
	// r.URL.Path for a plain (non-StripPrefix) handler registration; this
	// handler strips it itself.
	raw := strings.TrimPrefix(r.URL.Path, "/internal/featuregate/")
	raw = strings.Trim(raw, "/")
	tenantID, err := uuid.Parse(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}

	ctx := r.Context()
	flags := FlagsResponse{
		RAGEnabled:    h.resolver.IsEnabled(ctx, tenantID, settings.EnableRAG),
		VoiceEnabled:  h.resolver.IsEnabled(ctx, tenantID, settings.EnableVoice),
		RelayEnabled:  h.resolver.IsEnabled(ctx, tenantID, settings.EnableRelay),
		CoworkEnabled: h.resolver.IsEnabled(ctx, tenantID, settings.EnableCowork),
		// SSOEnabled is the logical OR of all three SSO provider keys so the
		// edge-api needs only one gate check regardless of which IdP is configured.
		SSOEnabled: h.resolver.IsEnabled(ctx, tenantID, settings.EnableSSOSaml) ||
			h.resolver.IsEnabled(ctx, tenantID, settings.EnableSSOGoogle) ||
			h.resolver.IsEnabled(ctx, tenantID, settings.EnableSSOMicrosoft),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(flags)
}

// writeError emits a JSON error body with a matching Content-Type header.
// http.Error always forces "text/plain", which mismatches the JSON body this
// handler otherwise returns; kept in sync with the provider-blind JSON error
// pattern used elsewhere in control-plane (e.g. internal/providers/http.go).
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: message})
}
