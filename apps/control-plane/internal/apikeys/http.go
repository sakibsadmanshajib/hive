package apikeys

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
)

func errIs(err, target error) bool { return errors.Is(err, target) }

func limitsResponse(l KeyLimits) map[string]interface{} {
	tiers := l.TierOverrides
	if tiers == nil {
		tiers = map[string]TierLimit{}
	}
	tierMap := make(map[string]map[string]int, len(tiers))
	for tier, lim := range tiers {
		tierMap[tier] = map[string]int{"rpm": lim.RPM, "tpm": lim.TPM}
	}
	return map[string]interface{}{
		"api_key_id":     l.APIKeyID.String(),
		"rpm":            l.RPM,
		"tpm":            l.TPM,
		"tier_overrides": tierMap,
	}
}

// Handler handles all API-key HTTP routes.
type Handler struct {
	svc        *Service
	accountSvc *accounts.Service
	testVC     *accounts.ViewerContext // non-nil in tests to bypass real accounts service
}

// NewHandler returns a new Handler.
func NewHandler(svc *Service, accountSvc *accounts.Service) *Handler {
	return &Handler{svc: svc, accountSvc: accountSvc}
}

// ServeHTTP dispatches requests to the appropriate sub-handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	base := "/api/v1/accounts/current/api-keys"

	switch {
	case r.Method == http.MethodGet && path == base:
		h.handleListKeys(w, r)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/limits"):
		h.handleGetLimits(w, r)
	case r.Method == http.MethodPut && strings.HasSuffix(path, "/limits"):
		h.handleUpdateLimits(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, base+"/"):
		h.handleGetKey(w, r)
	case r.Method == http.MethodPost && path == base:
		h.handleCreateKey(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/policy"):
		h.handleUpdatePolicy(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/rotate"):
		h.handleRotateKey(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/disable"):
		h.handleDisableKey(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/enable"):
		h.handleEnableKey(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/revoke"):
		h.handleRevokeKey(w, r)
	case r.Method == http.MethodPost && path == "/internal/apikeys/resolve":
		h.handleInternalResolve(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *Handler) handleGetKey(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r)
	if !ok {
		return
	}

	keyID, ok := extractKeyID(w, r)
	if !ok {
		return
	}

	view, err := h.svc.GetKeyView(r.Context(), vc.CurrentAccount.ID, keyID)
	if err != nil {
		handleKeyError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, keyViewItem(view))
}

// resolveViewerContext extracts the authenticated viewer and resolves the
// current account, enforcing the CanManageAPIKeys gate.
func (h *Handler) resolveViewerContext(w http.ResponseWriter, r *http.Request) (accounts.ViewerContext, bool) {
	// Test override — bypasses real accounts service in unit tests.
	if h.testVC != nil {
		if !h.testVC.Gates.CanManageAPIKeys {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "verified account owner required",
				"code":  "api_key_management_forbidden",
			})
			return accounts.ViewerContext{}, false
		}
		return *h.testVC, true
	}

	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return accounts.ViewerContext{}, false
	}

	requestedAccountID := parseAccountHeader(r)
	vc, err := h.accountSvc.EnsureViewerContext(r.Context(), viewer, requestedAccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return accounts.ViewerContext{}, false
	}

	if !vc.Gates.CanManageAPIKeys {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "verified account owner required",
			"code":  "api_key_management_forbidden",
		})
		return accounts.ViewerContext{}, false
	}

	return vc, true
}

func (h *Handler) handleListKeys(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r)
	if !ok {
		return
	}

	views, err := h.svc.ListKeyViews(r.Context(), vc.CurrentAccount.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	items := make([]map[string]interface{}, 0, len(views))
	for _, view := range views {
		items = append(items, keyViewItem(view))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
}

func (h *Handler) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r)
	if !ok {
		return
	}

	var body struct {
		Nickname  string  `json:"nickname"`
		ExpiresAt *string `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.Nickname == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nickname is required"})
		return
	}

	input := CreateKeyInput{Nickname: body.Nickname}
	if body.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *body.ExpiresAt)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expires_at must be RFC3339"})
			return
		}
		input.ExpiresAt = &t
	}

	result, err := h.svc.CreateKey(r.Context(), vc.CurrentAccount.ID, vc.User.ID, input)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	view, err := h.svc.GetKeyView(r.Context(), vc.CurrentAccount.ID, result.Key.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	resp := keyViewItem(view)
	resp["secret"] = result.Secret
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) handleRotateKey(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r)
	if !ok {
		return
	}

	keyID, ok := extractKeyID(w, r)
	if !ok {
		return
	}

	var body struct {
		Nickname  string  `json:"nickname"`
		ExpiresAt *string `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.Nickname == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nickname is required"})
		return
	}

	var expiresAt *time.Time
	if body.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *body.ExpiresAt)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expires_at must be RFC3339"})
			return
		}
		expiresAt = &t
	}

	result, err := h.svc.RotateKey(r.Context(), vc.CurrentAccount.ID, vc.User.ID, keyID, body.Nickname, expiresAt)
	if err != nil {
		handleKeyError(w, err)
		return
	}

	view, err := h.svc.GetKeyView(r.Context(), vc.CurrentAccount.ID, result.NewKey.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	newKeyResp := keyViewItem(view)
	newKeyResp["secret"] = result.Secret
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"old_key_id": result.OldKey.ID.String(),
		"new_key":    newKeyResp,
	})
}

func (h *Handler) handleDisableKey(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r)
	if !ok {
		return
	}

	keyID, ok := extractKeyID(w, r)
	if !ok {
		return
	}

	key, err := h.svc.DisableKey(r.Context(), vc.CurrentAccount.ID, vc.User.ID, keyID)
	if err != nil {
		handleKeyError(w, err)
		return
	}

	view, err := h.svc.GetKeyView(r.Context(), vc.CurrentAccount.ID, key.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, keyViewItem(view))
}

func (h *Handler) handleEnableKey(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r)
	if !ok {
		return
	}

	keyID, ok := extractKeyID(w, r)
	if !ok {
		return
	}

	key, err := h.svc.EnableKey(r.Context(), vc.CurrentAccount.ID, vc.User.ID, keyID)
	if err != nil {
		handleKeyError(w, err)
		return
	}

	view, err := h.svc.GetKeyView(r.Context(), vc.CurrentAccount.ID, key.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, keyViewItem(view))
}

func (h *Handler) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r)
	if !ok {
		return
	}

	keyID, ok := extractKeyID(w, r)
	if !ok {
		return
	}

	key, err := h.svc.RevokeKey(r.Context(), vc.CurrentAccount.ID, vc.User.ID, keyID)
	if err != nil {
		handleKeyError(w, err)
		return
	}

	view, err := h.svc.GetKeyView(r.Context(), vc.CurrentAccount.ID, key.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, keyViewItem(view))
}

func (h *Handler) handleUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r)
	if !ok {
		return
	}

	keyID, ok := extractKeyID(w, r)
	if !ok {
		return
	}

	var body struct {
		ExpiresAt          *string  `json:"expires_at"`
		AllowAllModels     *bool    `json:"allow_all_models"`
		AllowedGroupNames  []string `json:"allowed_group_names"`
		AllowedAliases     []string `json:"allowed_aliases"`
		DeniedAliases      []string `json:"denied_aliases"`
		BudgetKind         *string  `json:"budget_kind"`
		BudgetLimitCredits *int64   `json:"budget_limit_credits"`
		BudgetAnchorAt     *string  `json:"budget_anchor_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	input := UpdatePolicyInput{
		AllowAllModels:     body.AllowAllModels,
		AllowedGroupNames:  body.AllowedGroupNames,
		AllowedAliases:     body.AllowedAliases,
		DeniedAliases:      body.DeniedAliases,
		BudgetKind:         body.BudgetKind,
		BudgetLimitCredits: body.BudgetLimitCredits,
	}

	if body.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *body.ExpiresAt)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expires_at must be RFC3339"})
			return
		}
		input.ExpiresAt = &t
	}
	if body.BudgetAnchorAt != nil {
		t, err := time.Parse(time.RFC3339, *body.BudgetAnchorAt)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "budget_anchor_at must be RFC3339"})
			return
		}
		input.BudgetAnchorAt = &t
	}

	policy, err := h.svc.UpdatePolicy(r.Context(), vc.CurrentAccount.ID, vc.User.ID, keyID, input)
	if err != nil {
		handleKeyError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"api_key_id":       policy.APIKeyID.String(),
		"allow_all_models": policy.AllowAllModels,
		"budget_kind":      policy.BudgetKind,
		"policy_version":   policy.PolicyVersion,
	})
}

func (h *Handler) handleGetLimits(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r)
	if !ok {
		return
	}
	keyID, ok := extractKeyID(w, r)
	if !ok {
		return
	}
	limits, err := h.svc.GetLimits(r.Context(), vc.CurrentAccount.ID, keyID)
	if err != nil {
		handleKeyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, limitsResponse(limits))
}

func (h *Handler) handleUpdateLimits(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r)
	if !ok {
		return
	}
	keyID, ok := extractKeyID(w, r)
	if !ok {
		return
	}

	var body struct {
		RPM           int                  `json:"rpm"`
		TPM           int                  `json:"tpm"`
		TierOverrides map[string]TierLimit `json:"tier_overrides"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	limits, err := h.svc.UpdateLimits(r.Context(), vc.CurrentAccount.ID, keyID, KeyLimitsInput{
		RPM:           body.RPM,
		TPM:           body.TPM,
		TierOverrides: body.TierOverrides,
	})
	if err != nil {
		switch {
		case errIs(err, ErrLimitsOutOfRange):
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error": "rate-limit value out of range",
				"code":  "limits_out_of_range",
			})
		default:
			handleKeyError(w, err)
		}
		return
	}

	writeJSON(w, http.StatusOK, limitsResponse(limits))
}

func (h *Handler) handleInternalResolve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TokenHash string `json:"token_hash"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TokenHash == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token_hash is required"})
		return
	}

	snapshot, err := h.svc.ResolveSnapshot(r.Context(), body.TokenHash)
	if err != nil {
		handleKeyError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, snapshot)
}

// --- helpers ---

func parseAccountHeader(r *http.Request) uuid.UUID {
	val := r.Header.Get("X-Hive-Account-ID")
	if val == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(val)
	if err != nil {
		return uuid.Nil
	}
	return id
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// extractKeyID parses the key_id segment from the request path.
// Expected path: /api/v1/accounts/current/api-keys/{key_id}/{action}
func extractKeyID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	// Path: /api/v1/accounts/current/api-keys/{key_id}/{action}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/accounts/current/api-keys/"), "/")
	if len(parts) < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key_id required"})
		return uuid.Nil, false
	}
	id, err := uuid.Parse(parts[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid key_id"})
		return uuid.Nil, false
	}
	return id, true
}

func handleKeyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "key not found"})
	case errors.Is(err, ErrRevoked):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "key is revoked"})
	case errors.Is(err, ErrDisabled):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "key is not disabled"})
	case errors.Is(err, ErrNotActive):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "key is not active"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func keyViewItem(view KeyView) map[string]interface{} {
	item := map[string]interface{}{
		"id":              view.ID.String(),
		"nickname":        view.Nickname,
		"status":          string(view.Status),
		"redacted_suffix": view.RedactedSuffix,
		"created_at":      view.CreatedAt.Format(time.RFC3339),
		"updated_at":      view.UpdatedAt.Format(time.RFC3339),
		"expires_at":      formatTimestamp(view.ExpiresAt),
		"last_used_at":    formatTimestamp(view.LastUsedAt),
		"expiration_summary": map[string]interface{}{
			"kind":  view.ExpirationSummary.Kind,
			"label": view.ExpirationSummary.Label,
		},
		"budget_summary": map[string]interface{}{
			"kind":  view.BudgetSummary.Kind,
			"label": view.BudgetSummary.Label,
		},
		"allowlist_summary": map[string]interface{}{
			"mode":        view.AllowlistSummary.Mode,
			"group_names": view.AllowlistSummary.GroupNames,
			"label":       view.AllowlistSummary.Label,
		},
	}
	return item
}

// formatTimestamp formats a time pointer for JSON response, returning nil for nil.
func formatTimestamp(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339)
}

// keyIDFromPath extracts a UUID from the second-to-last path segment.
func keyIDFromPath(path, base string) (uuid.UUID, error) {
	suffix := strings.TrimPrefix(path, base+"/")
	parts := strings.SplitN(suffix, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return uuid.Nil, fmt.Errorf("missing key_id")
	}
	return uuid.Parse(parts[0])
}
