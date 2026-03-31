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
	case r.Method == http.MethodPost && path == base:
		h.handleCreateKey(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/rotate"):
		h.handleRotateKey(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/disable"):
		h.handleDisableKey(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/enable"):
		h.handleEnableKey(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/revoke"):
		h.handleRevokeKey(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
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
	item := map[string]interface{}{
		"id":              k.ID.String(),
		"nickname":        k.Nickname,
		"status":          string(k.Status),
		"redacted_suffix": k.RedactedSuffix,
		"created_at":      k.CreatedAt.Format(time.RFC3339),
		"updated_at":      k.UpdatedAt.Format(time.RFC3339),
	}
	if k.ExpiresAt != nil {
		item["expires_at"] = k.ExpiresAt.Format(time.RFC3339)
	}
	if k.LastUsedAt != nil {
		item["last_used_at"] = k.LastUsedAt.Format(time.RFC3339)
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
