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
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Extract {tenant_id} from the trailing path segment.
	// Route is registered as "/internal/featuregate/" so the mux strips the prefix.
	raw := strings.TrimPrefix(r.URL.Path, "/internal/featuregate/")
	raw = strings.Trim(raw, "/")
	tenantID, err := uuid.Parse(raw)
	if err != nil {
		http.Error(w, `{"error":"invalid tenant_id"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	flags := FlagsResponse{
		RAGEnabled:    h.resolver.IsEnabled(ctx, tenantID, settings.EnableRAG),
		VoiceEnabled:  h.resolver.IsEnabled(ctx, tenantID, settings.EnableVoice),
		RelayEnabled:  h.resolver.IsEnabled(ctx, tenantID, settings.EnableRelay),
		CoworkEnabled: h.resolver.IsEnabled(ctx, tenantID, settings.EnableCowork),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(flags)
}
