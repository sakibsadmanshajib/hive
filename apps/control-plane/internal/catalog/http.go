package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
)

// Handler serves catalog endpoints. The public catalog route reads the optional
// tenant claim from context (set by the auth middleware) and calls
// ListModelsForTenant so each tenant sees only their permitted aliases.
// Admin visibility routes are mounted separately via VisibilityMux() and must
// be wrapped with the shared-secret middleware before registration.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/internal/catalog/snapshot":
		h.handleSnapshot(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/catalog/models":
		h.handlePublicCatalog(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *Handler) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.svc.GetSnapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "catalog snapshot unavailable"})
		return
	}

	writeJSON(w, http.StatusOK, snapshot)
}

// handlePublicCatalog returns the model list filtered for the caller's tenant.
// If an auth.Viewer is present in context (OptionalRequire middleware ran) and
// its TenantID is non-nil, that tenant's visibility rules are applied.
// Otherwise only public/preview aliases are returned (unauthenticated path).
func (h *Handler) handlePublicCatalog(w http.ResponseWriter, r *http.Request) {
	tenantID := uuid.Nil
	if v, ok := auth.ViewerFromContext(r.Context()); ok && v.TenantID != uuid.Nil {
		tenantID = v.TenantID
	}

	aliases, err := h.svc.ListModelsForTenant(r.Context(), tenantID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "catalog unavailable"})
		return
	}

	models := buildPublicCatalogModels(aliases)
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// buildPublicCatalogModels converts a []ModelAlias slice to the wire shape
// returned by /api/v1/catalog/models. Internal aliases are omitted.
func buildPublicCatalogModels(aliases []ModelAlias) []PublicCatalogModel {
	out := make([]PublicCatalogModel, 0, len(aliases))
	for _, a := range aliases {
		if strings.EqualFold(a.Visibility, "internal") {
			continue
		}
		out = append(out, PublicCatalogModel{
			ID:               a.AliasID,
			DisplayName:      a.DisplayName,
			Summary:          a.Summary,
			CapabilityBadges: append([]string(nil), a.CapabilityBadges...),
			Pricing: CatalogPricing{
				InputPriceCredits:      a.InputPriceCredits,
				OutputPriceCredits:     a.OutputPriceCredits,
				CacheReadPriceCredits:  a.CacheReadPriceCredits,
				CacheWritePriceCredits: a.CacheWritePriceCredits,
			},
			Lifecycle: a.Lifecycle,
		})
	}
	return out
}

// OWUISync is satisfied by *owui.Client. Declared as an interface here so
// tests can inject a fake without importing the owui package.
//
// EnsureGroup creates or looks up the OWUI group by name and returns its
// OWUI-internal UUID. SyncModelAccessControl sets the per-model access_control
// object; passing a nil/empty allowedGroupIDs sends access_control:null
// (public). Callers must resolve group names to UUIDs via EnsureGroup before
// calling SyncModelAccessControl.
type OWUISync interface {
	EnsureGroup(ctx context.Context, name string) (string, error)
	SyncModelAccessControl(ctx context.Context, modelID string, allowedGroupIDs []string) error
}

// VisibilityHandler serves admin visibility endpoints for tenant_model_visibility.
// All routes must be protected by RequireInternalToken before registration.
//
//	PUT    /internal/catalog/visibility/{tenantID}/{aliasID}
//	DELETE /internal/catalog/visibility/{tenantID}/{aliasID}
//	GET    /internal/catalog/visibility/{tenantID}
type VisibilityHandler struct {
	svc  *Service
	owui OWUISync
}

// NewVisibilityHandler constructs a VisibilityHandler. Pass nil for owui to
// disable OWUI sync (e.g. when OWUI is not configured).
func NewVisibilityHandler(svc *Service, owui OWUISync) *VisibilityHandler {
	return &VisibilityHandler{svc: svc, owui: owui}
}

// VisibilityMux returns a handler that routes the three admin visibility endpoints.
func (h *VisibilityHandler) VisibilityMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/catalog/visibility/", h.handleVisibility)
	return mux
}

// handleVisibility dispatches PUT / DELETE / GET based on path segments.
// Path forms:
//
//	/internal/catalog/visibility/{tenantID}            → GET
//	/internal/catalog/visibility/{tenantID}/{aliasID}  → PUT or DELETE
func (h *VisibilityHandler) handleVisibility(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/internal/catalog/visibility/")
	parts := strings.SplitN(tail, "/", 2)

	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenantID required"})
		return
	}

	tenantID, err := uuid.Parse(parts[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenantID"})
		return
	}

	if len(parts) == 1 || parts[1] == "" {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		h.handleListVisibility(w, r, tenantID)
		return
	}

	aliasID := parts[1]
	switch r.Method {
	case http.MethodPut:
		h.handleUpsertVisibility(w, r, tenantID, aliasID)
	case http.MethodDelete:
		h.handleDeleteVisibility(w, r, tenantID, aliasID)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (h *VisibilityHandler) handleListVisibility(w http.ResponseWriter, r *http.Request, tenantID uuid.UUID) {
	rows, err := h.svc.repo.GetVisibilityRows(r.Context(), tenantID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list visibility rows"})
		return
	}
	if rows == nil {
		rows = []TenantModelVisibility{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
}

func (h *VisibilityHandler) handleUpsertVisibility(w http.ResponseWriter, r *http.Request, tenantID uuid.UUID, aliasID string) {
	var body struct {
		Visible bool `json:"visible"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	row := TenantModelVisibility{
		TenantID: tenantID,
		AliasID:  aliasID,
		Visible:  body.Visible,
	}
	if err := h.svc.repo.UpsertVisibility(r.Context(), row); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to upsert visibility"})
		return
	}

	h.syncOWUI(r, aliasID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *VisibilityHandler) handleDeleteVisibility(w http.ResponseWriter, r *http.Request, tenantID uuid.UUID, aliasID string) {
	if err := h.svc.repo.DeleteVisibility(r.Context(), tenantID, aliasID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete visibility"})
		return
	}

	h.syncOWUI(r, aliasID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// syncOWUI computes the full OWUI access_control state for aliasID after any
// visibility mutation and writes it atomically. It is best-effort: errors do
// not fail the HTTP response.
//
// Visibility semantics:
//   - restricted alias, no visible=true rows: model should be inaccessible in
//     OWUI. We send a non-nil but empty-named sentinel group so OWUI does not
//     fall back to public access (access_control:null = public for all users).
//     Concretely we use the group "hive-restricted-placeholder" which by
//     definition has no members.
//   - restricted alias, visible=true rows exist: resolve real OWUI group UUIDs
//     via EnsureGroup and send those as the allowlist.
//   - public/preview alias with visible=false rows (explicit blocks): the
//     model stays in OWUI as public (access_control:null) because OWUI cannot
//     express per-user deny lists. Hive's catalog filtering is the enforcement
//     layer for public-alias blocks; OWUI sync is skipped for public aliases.
func (h *VisibilityHandler) syncOWUI(r *http.Request, aliasID string) {
	if h.owui == nil {
		return
	}
	ctx := r.Context()

	alias, err := h.svc.repo.GetAlias(ctx, aliasID)
	if err != nil {
		return
	}

	// Public/preview aliases: Hive catalog is the enforcement layer for
	// visible=false blocks. OWUI has no deny-list primitive, so skip sync.
	if alias.Visibility != "restricted" {
		return
	}

	visibleRows, err := h.svc.repo.GetAllVisibleTenantsForAlias(ctx, aliasID)
	if err != nil {
		return
	}

	if len(visibleRows) == 0 {
		// Restricted alias with no active grants: lock it down in OWUI by
		// using a placeholder group that has no members. Sending null would
		// make it public, which is the opposite of the desired state.
		placeholderID, err := h.owui.EnsureGroup(ctx, "hive-restricted-placeholder")
		if err != nil {
			return
		}
		_ = h.owui.SyncModelAccessControl(ctx, aliasID, []string{placeholderID})
		return
	}

	// Resolve real OWUI group UUIDs for each tenant that has an active grant.
	groupIDs := make([]string, 0, len(visibleRows))
	for _, row := range visibleRows {
		gid, err := h.owui.EnsureGroup(ctx, "tenant_"+row.TenantID.String())
		if err != nil {
			return
		}
		groupIDs = append(groupIDs, gid)
	}
	_ = h.owui.SyncModelAccessControl(ctx, aliasID, groupIDs)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
