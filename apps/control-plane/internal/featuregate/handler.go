// Package featuregate exposes an internal service-to-service endpoint that
// returns per-tenant feature gates so edge-api can gate capabilities without
// querying the database directly.
//
// GET /internal/featuregate/{tenant_id}
//
// The tenant_id path segment is the UUID of the requesting tenant. The
// handler resolves the client-visible gate keys (categories agents and sso)
// from the tenant_settings table via the shared settings.Resolver in a
// single call. The response is a flat key->bool map; unknown/unset keys
// default to false. Admin, billing, and audit_sink gates are deliberately
// excluded here (issue #293 security review): this endpoint feeds edge-api
// and, through /v1/featuregate, Open WebUI, so it must never expose them.
//
// Data-model rework (issue #293): this used to be a hardcoded five-field
// FlagsResponse struct, so every new gate cost a change here plus a matching
// change in apps/edge-api/internal/featuregate/gate.go. Both sides now carry
// a dynamic map keyed by the tenant_setting_key string, so a new gate key
// never touches this file: add it to public.feature_gate_keys (and, for a
// genuinely new key, ALTER TYPE public.tenant_setting_key ADD VALUE) in a
// migration, and it appears here automatically.
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

// FlagsResponse is the JSON body returned to the edge-api: every gate key
// known to public.feature_gate_keys mapped to its enabled state for the
// requesting tenant.
type FlagsResponse struct {
	Gates map[string]bool `json:"gates"`
}

// Resolver is the narrow interface the handler needs from settings.Resolver.
// ClientVisibleEnabled resolves only the client-visible gate keys (agents, sso)
// for tenantID in one call; see settings.Resolver.ClientVisibleEnabled. The
// full set (settings.Resolver.AllEnabled) is intentionally not used here so
// admin/billing/audit_sink gates cannot leak to the client.
type Resolver interface {
	ClientVisibleEnabled(ctx context.Context, tenantID uuid.UUID) (map[settings.Key]bool, error)
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

	enabled, err := h.resolver.ClientVisibleEnabled(r.Context(), tenantID)
	if err != nil {
		// Provider-blind: the upstream error (DB outage, query failure) never
		// reaches the response body.
		writeError(w, http.StatusInternalServerError, "failed to resolve feature gates")
		return
	}

	gates := make(map[string]bool, len(enabled))
	for key, on := range enabled {
		gates[string(key)] = on
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(FlagsResponse{Gates: gates})
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
