package byok

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/authz"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// Handler serves the BYOK surfaces.
type Handler struct {
	svc        *Service
	accountSvc *accounts.Service
	roleSvc    *platform.RoleService
	policy     authz.Policy
	testVC     *accounts.ViewerContext
	testActor  *authz.Actor
}

// NewHandler returns a Handler over the given service. Chain
// WithAccountService and WithRoleService for production wiring; unit tests
// install a canned viewer context instead.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc, policy: authz.NewPolicy()}
}

// WithAccountService wires real viewer-context resolution.
func (h *Handler) WithAccountService(a *accounts.Service) *Handler {
	cloned := *h
	cloned.accountSvc = a
	return &cloned
}

// WithRoleService wires the platform-admin overlay lookup.
func (h *Handler) WithRoleService(r *platform.RoleService) *Handler {
	cloned := *h
	cloned.roleSvc = r
	return &cloned
}

const tenantPrefix = "/api/v1/accounts/current/provider-keys"
const adminPrefix = "/api/v1/admin/provider-keys"

// maxRegisterBodyBytes caps the register body decode. A credential is a few
// hundred bytes; 64 KiB is generous headroom and matches the payments
// handler's cap posture for untrusted bodies.
const maxRegisterBodyBytes = 64 << 10

// TenantMux routes the tenant-facing surface:
//
//	POST   /api/v1/accounts/current/provider-keys            register
//	GET    /api/v1/accounts/current/provider-keys            list (masked)
//	POST   /api/v1/accounts/current/provider-keys/{id}/revoke
//
// Mount behind AuthMiddleware.Require.
func (h *Handler) TenantMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(tenantPrefix, h.handleCollection)
	mux.HandleFunc(tenantPrefix+"/", h.handleItem)
	return mux
}

// AdminMux routes the platform-admin surface:
//
//	GET /api/v1/admin/provider-keys           all keys, masked
//	GET /api/v1/admin/provider-keys?account_id={uuid}
//
// Mount behind AuthMiddleware.Require + RequirePlatformAdmin.
func (h *Handler) AdminMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(adminPrefix, h.handleAdminList)
	// The router hands this mux the whole /api/v1/admin/provider-keys/ subtree,
	// so the exact trailing-slash form has to be served here; registering only
	// the bare prefix made the nested mux answer 404 for a path the outer one
	// had already accepted. Anything deeper is not a route (there is no
	// single-item admin endpoint), and it must not quietly return the full
	// cross-tenant list under a URL shaped like one.
	mux.HandleFunc(adminPrefix+"/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != adminPrefix+"/" {
			writeError(w, http.StatusNotFound, "not_found", "unknown route")
			return
		}
		h.handleAdminList(w, r)
	})
	return mux
}

func (h *Handler) handleCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.handleRegister(w, r)
	case http.MethodGet:
		h.handleList(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (h *Handler) handleItem(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, tenantPrefix+"/")
	parts := strings.Split(strings.Trim(suffix, "/"), "/")
	if len(parts) != 2 || parts[1] != "revoke" {
		writeError(w, http.StatusNotFound, "not_found", "unknown route")
		return
	}
	keyID, err := uuid.Parse(parts[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "provider key id must be a UUID")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	h.handleRevoke(w, r, keyID)
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r, authz.PermProviderKeysWrite)
	if !ok {
		return
	}

	var body struct {
		Label        string            `json:"label"`
		ProviderSlug *string           `json:"provider_slug"`
		BaseURL      *string           `json:"base_url"`
		APIKey       string            `json:"api_key"`
		ModelMap     map[string]string `json:"model_map"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRegisterBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON under 64 KiB")
		return
	}

	key, err := h.svc.Register(r.Context(), vc.CurrentAccount.ID, vc.User.ID, RegisterInput{
		Label:        body.Label,
		ProviderSlug: body.ProviderSlug,
		BaseURL:      body.BaseURL,
		APIKey:       body.APIKey,
		ModelMap:     body.ModelMap,
	})
	if err != nil {
		mapServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, keyView(key))
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r, authz.PermProviderKeysRead)
	if !ok {
		return
	}
	keys, err := h.svc.List(r.Context(), vc.CurrentAccount.ID)
	if err != nil {
		writeOpaque(w, "request could not be completed", err)
		return
	}
	items := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		items = append(items, keyView(k))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) handleRevoke(w http.ResponseWriter, r *http.Request, keyID uuid.UUID) {
	vc, ok := h.resolveViewerContext(w, r, authz.PermProviderKeysWrite)
	if !ok {
		return
	}
	key, err := h.svc.Revoke(r.Context(), vc.CurrentAccount.ID, keyID, vc.User.ID)
	if err != nil {
		mapServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, keyView(key))
}

func (h *Handler) handleAdminList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	// Defense in depth: the router wraps AdminMux with RequirePlatformAdmin,
	// but this handler enforces the same permission itself so a future mount
	// that skips the middleware cannot expose every tenant's key metadata.
	if _, ok := h.resolveViewerContext(w, r, authz.PermPlatformAdmin); !ok {
		return
	}

	var (
		keys []Key
		err  error
	)
	if raw := r.URL.Query().Get("account_id"); raw != "" {
		accountID, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_account_id", "account_id must be a UUID")
			return
		}
		keys, err = h.svc.List(r.Context(), accountID)
	} else {
		keys, err = h.svc.ListAll(r.Context())
	}
	if err != nil {
		writeOpaque(w, "request could not be completed", err)
		return
	}
	items := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		items = append(items, keyView(k))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// resolveViewerContext mirrors internal/apikeys: resolve viewer + current
// account, then enforce perm via policy.Can with the platform-admin overlay.
func (h *Handler) resolveViewerContext(w http.ResponseWriter, r *http.Request, perm authz.Permission) (accounts.ViewerContext, bool) {
	if h.testVC != nil {
		actor := h.testActor
		if actor == nil {
			a := accounts.ActorFor(authFromVC(h.testVC), accounts.Membership{
				AccountID: h.testVC.CurrentAccount.ID,
				UserID:    h.testVC.User.ID,
				Role:      h.testVC.CurrentAccount.Role,
				Status:    accounts.StatusActive,
			}, false)
			actor = &a
		}
		if !h.policy.Can(*actor, perm) {
			writeError(w, http.StatusForbidden, "provider_key_management_forbidden",
				"verified account owner required")
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
		writeOpaque(w, "request could not be completed", err)
		return accounts.ViewerContext{}, false
	}

	isAdmin := false
	if h.roleSvc != nil {
		admin, err := h.roleSvc.IsPlatformAdmin(r.Context(), viewer.UserID)
		if err != nil {
			slog.ErrorContext(r.Context(), "byok: platform-admin lookup failed",
				slog.String("user_id", viewer.UserID.String()),
				slog.String("err", err.Error()))
			writeError(w, http.StatusInternalServerError, "authorization_unavailable",
				"authorization unavailable")
			return accounts.ViewerContext{}, false
		}
		isAdmin = admin
	}
	actor := accounts.ActorFor(viewer, accounts.Membership{
		AccountID: vc.CurrentAccount.ID,
		UserID:    viewer.UserID,
		Role:      vc.CurrentAccount.Role,
		Status:    accounts.StatusActive,
	}, isAdmin)
	if !h.policy.Can(actor, perm) {
		writeError(w, http.StatusForbidden, "provider_key_management_forbidden",
			"verified account owner required")
		return accounts.ViewerContext{}, false
	}

	return vc, true
}

// authFromVC reconstructs a minimal auth.Viewer from a ViewerContext for test
// Actor construction.
func authFromVC(vc *accounts.ViewerContext) auth.Viewer {
	return auth.Viewer{
		UserID:        vc.User.ID,
		Email:         vc.User.Email,
		EmailVerified: vc.User.EmailVerified,
	}
}

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

// mapServiceError translates sentinel errors to HTTP status codes. Locked
// mode (no encryption key configured) is a 503, never a silent plaintext
// fallback.
func mapServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
	case errors.Is(err, ErrNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "byok_not_configured",
			"BYOK is not configured on this deployment")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "provider_key_conflict",
			"an active key for that provider is already registered")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "provider key not found")
	default:
		writeOpaque(w, "request could not be completed", err)
	}
}

// keyView builds the customer-safe JSON view of a key row. EncryptedAPIKey is
// deliberately absent: no endpoint in this package ever returns key material,
// encrypted or otherwise.
func keyView(k Key) map[string]any {
	view := map[string]any{
		"id":         k.ID.String(),
		"account_id": k.AccountID.String(),
		"label":      k.Label,
		"key_last4":  k.KeyLast4,
		"status":     k.Status,
		"model_map":  k.ModelMap,
		"created_by": k.CreatedBy.String(),
		"created_at": k.CreatedAt.Format(time.RFC3339),
		"updated_at": k.UpdatedAt.Format(time.RFC3339),
	}
	if k.ProviderSlug != nil {
		view["provider_slug"] = *k.ProviderSlug
	} else {
		view["base_url"] = deref(k.BaseURL)
	}
	if k.RevokedAt != nil {
		view["revoked_at"] = k.RevokedAt.Format(time.RFC3339)
	}
	return view
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func writeOpaque(w http.ResponseWriter, msg string, err error) {
	slog.Error(msg, slog.String("err", err.Error()))
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
}

type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: message, Code: code})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
