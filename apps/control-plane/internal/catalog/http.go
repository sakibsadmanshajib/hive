package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenants"
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
// If a tenants.User is present in context (JWT auth middleware ran), its
// TenantID is used. If not present, only public aliases are returned
// (unauthenticated / public API key path).
func (h *Handler) handlePublicCatalog(w http.ResponseWriter, r *http.Request) {
	tenantID := uuid.Nil
	if u, ok := tenants.UserFrom(r.Context()); ok {
		tenantID = u.TenantID
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
type OWUISync interface {
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

// syncOWUI computes the full allowlist for aliasID and calls SyncModelAccessControl.
// Errors are logged but do not fail the HTTP response: OWUI sync is best-effort.
func (h *VisibilityHandler) syncOWUI(r *http.Request, aliasID string) {
	if h.owui == nil {
		return
	}
	visibleRows, err := h.svc.repo.GetAllVisibleTenantsForAlias(r.Context(), aliasID)
	if err != nil {
		return
	}
	groupIDs := make([]string, 0, len(visibleRows))
	for _, row := range visibleRows {
		groupIDs = append(groupIDs, "tenant_"+row.TenantID.String())
	}
	_ = h.owui.SyncModelAccessControl(r.Context(), aliasID, groupIDs)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
