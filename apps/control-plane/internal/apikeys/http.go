package apikeys

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
)

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

	key, err := h.svc.GetKey(r.Context(), vc.CurrentAccount.ID, keyID)
	if err != nil {
		handleKeyError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, keyListItem(key))
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

	keys, err := h.svc.ListKeys(r.Context(), vc.CurrentAccount.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	items := make([]map[string]interface{}, 0, len(keys))
	for _, k := range keys {
		items = append(items, keyListItem(k))
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

	resp := keyListItem(result.Key)
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

	newKeyResp := keyListItem(result.NewKey)
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

	writeJSON(w, http.StatusOK, keyListItem(key))
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

	writeJSON(w, http.StatusOK, keyListItem(key))
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

	writeJSON(w, http.StatusOK, keyListItem(key))
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
	switch err {
	case ErrNotFound:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "key not found"})
	case ErrRevoked:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "key is revoked"})
	case ErrDisabled:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "key is not disabled"})
	case ErrNotActive:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "key is not active"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func keyListItem(k APIKey) map[string]interface{} {
	k = applyExpiry(k, time.Now())

	item := map[string]interface{}{
		"id":                 k.ID.String(),
		"nickname":           k.Nickname,
		"status":             string(k.Status),
		"redacted_suffix":    k.RedactedSuffix,
		"created_at":         k.CreatedAt.Format(time.RFC3339),
		"updated_at":         k.UpdatedAt.Format(time.RFC3339),
		"expires_at":         formatTimestamp(k.ExpiresAt),
		"last_used_at":       formatTimestamp(k.LastUsedAt),
		"expiration_summary": expirationSummary(k),
		"budget_summary": map[string]interface{}{
			"kind":  "none",
			"label": "No budget cap",
		},
		"allowlist_summary": map[string]interface{}{
			"mode":        "groups",
			"group_names": []string{"default"},
			"label":       "Default launch-safe models",
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

func expirationSummary(k APIKey) map[string]interface{} {
	if k.ExpiresAt == nil {
		return map[string]interface{}{
			"kind":  "never",
			"label": "Never expires",
		}
	}

	if k.Status == KeyStatusExpired {
		return map[string]interface{}{
			"kind":  "expired",
			"label": "Expired",
		}
	}

	return map[string]interface{}{
		"kind":  "scheduled",
		"label": "Expires " + k.ExpiresAt.Format(time.RFC3339),
	}
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
