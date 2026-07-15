// Admin feature-gate surface (issue #292, agent-subsystem blueprint Step 1.2).
//
// The internal handler in handler.go answers edge-api service-to-service reads.
// This file is the human-facing side: an owner-gated admin API the web-console
// admin page uses to list every registered gate and flip it per tenant.
//
//	GET /api/v1/admin/feature-gates          — registry joined with this
//	                                            tenant's enablement
//	PUT /api/v1/admin/feature-gates/{key}    — toggle one gate for this tenant
//
// The tenant is the caller's selected tenant (auth.Viewer.TenantID) and the
// write is attributed to the caller (auth.Viewer.UserID). Both routes are
// mounted behind auth.Middleware.Require + RoleService.RequirePlatformAdmin in
// router.go, so this handler assumes an authenticated platform admin; it reads
// the viewer only for the tenant scope and write attribution.
package featuregate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenant/settings"
)

// adminPrefix is the exact collection path; the item path is adminPrefix+"/{key}".
const adminPrefix = "/api/v1/admin/feature-gates"

// AdminStore is the narrow settings surface the admin handler needs.
// *settings.Resolver satisfies it.
type AdminStore interface {
	Registry(ctx context.Context) ([]settings.GateKey, error)
	AllEnabled(ctx context.Context, tenantID uuid.UUID) (map[settings.Key]bool, error)
	Set(ctx context.Context, tenantID uuid.UUID, key settings.Key, enabled bool, updatedBy uuid.UUID) error
}

// AdminHandler serves the owner-gated admin feature-gate routes.
type AdminHandler struct {
	store AdminStore
}

// NewAdminHandler constructs an AdminHandler. store must not be nil.
func NewAdminHandler(store AdminStore) *AdminHandler {
	return &AdminHandler{store: store}
}

// AdminMux routes the collection and item paths. Callers mount it behind the
// auth + platform-admin middleware (see router.go).
func (h *AdminHandler) AdminMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(adminPrefix, h.handleCollection)
	mux.HandleFunc(adminPrefix+"/", h.handleItem)
	return mux
}

// adminGate is one row the admin UI renders: the key, its human label and
// category, and whether it is enabled for the current tenant.
type adminGate struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Category string `json:"category"`
	Enabled  bool   `json:"enabled"`
}

type adminGatesResponse struct {
	Gates []adminGate `json:"gates"`
}

type setGateRequest struct {
	Enabled bool `json:"enabled"`
}

type setGateResponse struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

// handleCollection serves GET /api/v1/admin/feature-gates.
func (h *AdminHandler) handleCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenantID, _, ok := requireTenant(w, r)
	if !ok {
		return
	}

	ctx := r.Context()
	registry, err := h.store.Registry(ctx)
	if err != nil {
		// Provider-blind: the upstream error never reaches the response body.
		writeError(w, http.StatusInternalServerError, "failed to load feature gate registry")
		return
	}
	enabled, err := h.store.AllEnabled(ctx, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve feature gates")
		return
	}

	// Registry is already ordered by (category, label); preserve that order.
	gates := make([]adminGate, 0, len(registry))
	for _, g := range registry {
		gates = append(gates, adminGate{
			Key:      string(g.Key),
			Label:    g.Label,
			Category: g.Category,
			Enabled:  enabled[g.Key],
		})
	}

	writeJSON(w, http.StatusOK, adminGatesResponse{Gates: gates})
}

// handleItem serves PUT /api/v1/admin/feature-gates/{key}.
func (h *AdminHandler) handleItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tenantID, userID, ok := requireTenant(w, r)
	if !ok {
		return
	}

	key := strings.Trim(strings.TrimPrefix(r.URL.Path, adminPrefix+"/"), "/")
	if key == "" {
		writeError(w, http.StatusBadRequest, "missing gate key")
		return
	}

	var req setGateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}

	err := h.store.Set(r.Context(), tenantID, settings.Key(key), req.Enabled, userID)
	if errors.Is(err, settings.ErrUnknownGateKey) {
		writeError(w, http.StatusBadRequest, "unknown feature gate key")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update feature gate")
		return
	}

	writeJSON(w, http.StatusOK, setGateResponse{Key: key, Enabled: req.Enabled})
}

// requireTenant pulls the viewer from context and returns its tenant + user id.
// The middleware guarantees an authenticated platform admin, but the viewer may
// still have no selected tenant (uuid.Nil), which is a 400 the operator fixes by
// switching workspace.
func requireTenant(w http.ResponseWriter, r *http.Request) (tenantID, userID uuid.UUID, ok bool) {
	viewer, present := auth.ViewerFromContext(r.Context())
	if !present {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return uuid.Nil, uuid.Nil, false
	}
	if viewer.TenantID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "no tenant selected")
		return uuid.Nil, uuid.Nil, false
	}
	return viewer.TenantID, viewer.UserID, true
}

// writeJSON emits a JSON body with a matching Content-Type header, mirroring
// writeError in handler.go.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
