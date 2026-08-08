// Admin feature-gate surface (issue #292, agent-subsystem blueprint Step 1.2).
//
// The internal handler in handler.go answers edge-api service-to-service reads.
// This file is the human-facing side: the admin API the web-console
// admin page uses to list every registered gate and flip it for the workspace.
//
//	GET /api/v1/admin/feature-gates          — registry joined with this
//	                                            tenant's enablement
//	PUT /api/v1/admin/feature-gates/{key}    — toggle one gate for this tenant
//
// The tenant is the caller's selected tenant (auth.Viewer.TenantID) and the
// write is attributed to the caller (auth.Viewer.UserID). Both routes are
// mounted behind auth.Middleware.Require plus platform.WorkspaceAdminGate in
// router.go, so this handler assumes a caller who administers the workspace in
// scope: its OWNER, or a platform admin (issue #758). The viewer supplies the
// tenant scope and the write attribution.
//
// One carve-out stays platform-only. A gate whose registry category is listed
// in platformManagedCategories describes the deployment rather than the
// workspace: the billing category carries plan entitlements such as
// ENABLE_EXTRA_USAGE, and the admin category carries ENABLE_PROVIDER_CUSTOM,
// the switch for custom provider endpoints. Neither is a customer decision, so
// writing one still requires the platform-admin overlay that
// platform.WorkspaceAdminGate stamps into the request context. Reads stay open
// to the workspace owner, and every row carries a manageable flag so the
// console can render those rows read-only instead of offering a control that
// would be refused.
package featuregate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenant/settings"
)

// adminPrefix is the exact collection path; the item path is adminPrefix+"/{key}".
const adminPrefix = "/api/v1/admin/feature-gates"

// platformManagedCategories lists the registry categories whose gates only a
// platform admin may write. Keyed by category rather than by key so a gate
// added by a later migration inherits the right posture without a code change.
var platformManagedCategories = map[string]bool{
	"billing": true, // plan entitlements: a workspace must not widen its own spend
	"admin":   true, // deployment shape, including custom provider endpoints
}

// manageableBy reports whether a caller may toggle a gate in category.
func manageableBy(category string, isPlatformAdmin bool) bool {
	if isPlatformAdmin {
		return true
	}
	return !platformManagedCategories[category]
}

// AdminStore is the narrow settings surface the admin handler needs.
// *settings.Resolver satisfies it.
type AdminStore interface {
	Registry(ctx context.Context) ([]settings.GateKey, error)
	AllEnabled(ctx context.Context, tenantID uuid.UUID) (map[settings.Key]bool, error)
	Set(ctx context.Context, tenantID uuid.UUID, key settings.Key, enabled bool, updatedBy uuid.UUID) error
}

// AdminHandler serves the workspace-administrator feature-gate routes.
type AdminHandler struct {
	store AdminStore
}

// NewAdminHandler constructs an AdminHandler. store must not be nil.
func NewAdminHandler(store AdminStore) *AdminHandler {
	return &AdminHandler{store: store}
}

// AdminMux routes the collection and item paths. Callers mount it behind the
// auth + workspace-admin middleware (see router.go).
func (h *AdminHandler) AdminMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(adminPrefix, h.handleCollection)
	mux.HandleFunc(adminPrefix+"/", h.handleItem)
	return mux
}

// adminGate is one row the admin UI renders: the key, its human label and
// category, whether it is enabled for the current tenant, and whether this
// caller may change it.
type adminGate struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Category   string `json:"category"`
	Enabled    bool   `json:"enabled"`
	Manageable bool   `json:"manageable"`
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

	isPlatformAdmin := platform.PlatformAdminFromContext(ctx)

	// Registry is already ordered by (category, label); preserve that order.
	gates := make([]adminGate, 0, len(registry))
	for _, g := range registry {
		gates = append(gates, adminGate{
			Key:        string(g.Key),
			Label:      g.Label,
			Category:   g.Category,
			Enabled:    enabled[g.Key],
			Manageable: manageableBy(g.Category, isPlatformAdmin),
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

	// A platform-managed gate is refused before the write, whatever authority the
	// caller holds over the workspace. An unregistered key has no category to
	// consult and falls through to Set, which rejects it as unknown.
	if !platform.PlatformAdminFromContext(r.Context()) {
		category, catErr := h.categoryOf(r.Context(), settings.Key(key))
		if catErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to load feature gate registry")
			return
		}
		if !manageableBy(category, false) {
			writeError(w, http.StatusForbidden, "platform admin permission required")
			return
		}
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

// categoryOf resolves the registry category of a gate key, or "" when the key
// is not registered.
func (h *AdminHandler) categoryOf(ctx context.Context, key settings.Key) (string, error) {
	registry, err := h.store.Registry(ctx)
	if err != nil {
		return "", err
	}
	for _, g := range registry {
		if g.Key == key {
			return g.Category, nil
		}
	}
	return "", nil
}

// requireTenant pulls the viewer from context and returns its tenant + user id.
// The middleware guarantees a caller who administers the workspace, but the
// viewer may
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
