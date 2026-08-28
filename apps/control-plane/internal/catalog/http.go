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
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, tenantSnapshotPrefix):
		h.handleTenantSnapshot(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/catalog/models":
		h.handlePublicCatalog(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

// tenantSnapshotPrefix is the internal, shared-secret-gated snapshot route
// scoped to one tenant: /internal/catalog/snapshot/tenant/{tenantID}.
//
// The tenant travels as a path segment parsed with uuid.Parse (never a body
// field or query parameter) because the only caller is edge-api, which fills it
// from auth.TenantID(ctx) on an already-authenticated request.
const tenantSnapshotPrefix = "/internal/catalog/snapshot/tenant/"

func (h *Handler) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.svc.GetSnapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "catalog snapshot unavailable"})
		return
	}

	writeJSON(w, http.StatusOK, snapshot)
}

// handleTenantSnapshot returns the catalog snapshot filtered to one tenant's
// entitlement, so /v1/models can serve a list that matches what the tenant may
// actually invoke.
func (h *Handler) handleTenantSnapshot(w http.ResponseWriter, r *http.Request) {
	raw := strings.Trim(strings.TrimPrefix(r.URL.Path, tenantSnapshotPrefix), "/")
	tenantID, err := uuid.Parse(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant id"})
		return
	}

	snapshot, err := h.svc.GetSnapshotForTenant(r.Context(), tenantID)
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
		// Same provider-blindness boundary buildCatalogSnapshot applies. This
		// endpoint is a second wire shape over the same rows and is reachable
		// without a bearer token, so it cannot inherit the other one's guard
		// by accident (issue #1284).
		a = redactAlias(a)
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
				// Without these two the wire always said pricing_mode "", which
				// is not a value the database CHECK can ever produce, and every
				// consumer would have read a variable-price alias as fixed.
				PricingMode:                a.PricingMode,
				ReservationEstimateCredits: a.ReservationEstimateCredits,
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

	h.syncOWUI(r.Context(), aliasID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *VisibilityHandler) handleDeleteVisibility(w http.ResponseWriter, r *http.Request, tenantID uuid.UUID, aliasID string) {
	if err := h.svc.repo.DeleteVisibility(r.Context(), tenantID, aliasID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete visibility"})
		return
	}

	h.syncOWUI(r.Context(), aliasID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// nonChatModalityBadges lists the model_aliases.capability_badges values that
// mark an alias as something other than a text chat model (embeddings and
// speech aliases). Open WebUI's model picker is chat-only; an alias carrying
// any of these badges must never be chat-selectable there, independent of
// its tenant_model_visibility class (issue #772: hive-embedding-default,
// hive-stt, hive-tts were listed as pickable chat models and produced a
// broken conversation when chosen).
//
// ponytail: capability_badges is freeform jsonb with no CHECK constraint and
// model_aliases has no dedicated modality/is_chat_model column, so this is a
// string-membership test over admin-editable data rather than a schema
// guarantee. It is bounded today: every embeddings/stt/tts alias is seeded by
// a migration under our control (20260423_01_embedding_alias.sql,
// 20260717_02_voice_groq_stt_tts.sql) and TestIsNonChatModality pins the
// badges each one carries. The real fix is a modality or is_chat_model column
// on model_aliases; add it and switch this predicate to read it if
// capability_badges ever drifts from these values.
var nonChatModalityBadges = map[string]bool{
	"embeddings": true,
	"stt":        true,
	"tts":        true,
	"voice":      true,
}

// isNonChatModality reports whether badges marks an alias as non-chat. This
// is deliberately exclusion logic (does any badge match a known non-chat
// tag) rather than inclusion logic (does "chat" appear): hive-auto is a
// legitimate chat-selectable alias whose badges are
// ["auto","fallback","preview"] with no explicit "chat" badge, so a
// chat-badge whitelist would wrongly hide it from the OWUI picker.
func isNonChatModality(badges []string) bool {
	for _, b := range badges {
		if nonChatModalityBadges[strings.ToLower(strings.TrimSpace(b))] {
			return true
		}
	}
	return false
}

// syncOWUI computes the full OWUI access_control state for aliasID and writes
// it atomically. It runs after any visibility mutation and from the boot-time
// ReconcileOWUISync (reconcile.go). It is best-effort: errors do not fail the
// caller.
//
// Semantics:
//   - non-chat-modality alias (isNonChatModality — embeddings/stt/tts/voice):
//     always locked out of the OWUI chat picker via the placeholder group,
//     regardless of Visibility class or any tenant_model_visibility grant.
//     tenant_model_visibility governs API invocation entitlement, not chat
//     dropdown selectability, and these aliases must never be chat-selectable
//     (issue #772).
//   - restricted chat alias, no visible=true rows: model should be
//     inaccessible in OWUI. We send a non-nil but empty-named sentinel group
//     so OWUI does not fall back to public access (access_control:null =
//     public for all users). Concretely we use the group
//     "hive-restricted-placeholder" which by definition has no members.
//   - restricted chat alias, visible=true rows exist: resolve real OWUI group
//     UUIDs via EnsureGroup and send those as the allowlist.
//   - public/preview chat alias with visible=false rows (explicit blocks):
//     the model stays in OWUI as public (access_control:null) because OWUI
//     cannot express per-user deny lists. Hive's catalog filtering is the
//     enforcement layer for public-alias blocks; OWUI sync is skipped for
//     public chat aliases.
func (h *VisibilityHandler) syncOWUI(ctx context.Context, aliasID string) {
	if h.owui == nil {
		return
	}

	alias, err := h.svc.repo.GetAlias(ctx, aliasID)
	if err != nil {
		return
	}

	if isNonChatModality(alias.CapabilityBadges) {
		h.lockOWUIModel(ctx, aliasID)
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
		// Restricted alias with no active grants: lock it down in OWUI.
		h.lockOWUIModel(ctx, aliasID)
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

// lockOWUIModel points aliasID's OWUI access_control at the
// "hive-restricted-placeholder" group, a group that by definition has no
// members. Sending a nil/empty allowlist instead would make the model public
// (OWUI's access_control:null semantics), which is the opposite of "locked
// out". Shared by the non-chat-modality lock and the restricted-with-no-
// grants lock in syncOWUI.
func (h *VisibilityHandler) lockOWUIModel(ctx context.Context, aliasID string) {
	placeholderID, err := h.owui.EnsureGroup(ctx, "hive-restricted-placeholder")
	if err != nil {
		return
	}
	_ = h.owui.SyncModelAccessControl(ctx, aliasID, []string{placeholderID})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
