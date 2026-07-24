package profiles

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/authz"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// Handler handles all profile-related HTTP routes.
type Handler struct {
	svc         *Service
	accountsSvc *accounts.Service
	roleSvc     *platform.RoleService // optional — used to populate Actor.IsAdmin via IsPlatformAdmin
	policy      authz.Policy
}

// NewHandler returns a new profiles Handler.
func NewHandler(svc *Service, accountsSvc *accounts.Service) *Handler {
	return &Handler{svc: svc, accountsSvc: accountsSvc, policy: authz.NewPolicy()}
}

// WithRoleService returns a copy of the handler wired with the platform role
// service so the admin overlay is enabled for Actor construction. Without it,
// Actor.IsAdmin is always false and platform admins cannot manage billing
// profiles via this handler unless they are also a verified workspace owner.
func (h *Handler) WithRoleService(roleSvc *platform.RoleService) *Handler {
	cloned := *h
	cloned.roleSvc = roleSvc
	return &cloned
}

// ServeHTTP dispatches requests to the appropriate sub-handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/current/profile":
		h.handleGetCurrentProfile(w, r)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/accounts/current/profile":
		h.handleUpdateCurrentProfile(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/current/billing-profile":
		h.handleGetCurrentBillingProfile(w, r)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/accounts/current/billing-profile":
		h.handleUpdateCurrentBillingProfile(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *Handler) handleGetCurrentProfile(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	profile, err := h.svc.GetAccountProfile(r.Context(), accountID)
	if err != nil {
		writeProfileError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, profile)
}

func (h *Handler) handleUpdateCurrentProfile(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	var input UpdateAccountProfileInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	profile, err := h.svc.UpdateAccountProfile(r.Context(), accountID, input)
	if err != nil {
		writeProfileError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, profile)
}

func (h *Handler) handleGetCurrentBillingProfile(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveVerifiedCurrentAccountID(w, r)
	if !ok {
		return
	}

	profile, err := h.svc.GetBillingProfile(r.Context(), accountID)
	if err != nil {
		writeProfileError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, profile)
}

func (h *Handler) handleUpdateCurrentBillingProfile(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveVerifiedCurrentAccountID(w, r)
	if !ok {
		return
	}

	var input UpdateBillingProfileInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	profile, err := h.svc.UpdateBillingProfile(r.Context(), accountID, input)
	if err != nil {
		writeProfileError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, profile)
}

func (h *Handler) resolveCurrentAccountID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	viewerContext, ok := h.resolveViewerContext(w, r)
	if !ok {
		return uuid.Nil, false
	}

	return viewerContext.CurrentAccount.ID, true
}

func (h *Handler) resolveVerifiedCurrentAccountID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	viewerContext, ok := h.resolveViewerContext(w, r)
	if !ok {
		return uuid.Nil, false
	}

	// Phase 18: route authz through policy.Can — replaces bare EmailVerified check.
	// Actor is built from the already-resolved viewer context fields. isAdmin
	// resolves the real platform-admin overlay when roleSvc is wired (see
	// WithRoleService); without it, a real platform admin who is not a
	// verified workspace owner is silently denied billing access here.
	isAdmin := false
	if h.roleSvc != nil {
		admin, err := h.roleSvc.IsPlatformAdmin(r.Context(), viewerContext.User.ID)
		if err != nil {
			slog.ErrorContext(r.Context(), "profiles: platform-admin lookup failed",
				slog.String("user_id", viewerContext.User.ID.String()),
				slog.String("err", err.Error()))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "authorization unavailable"})
			return uuid.Nil, false
		}
		isAdmin = admin
	}
	actor := accounts.ActorFor(
		auth.Viewer{
			UserID:        viewerContext.User.ID,
			Email:         viewerContext.User.Email,
			EmailVerified: viewerContext.User.EmailVerified,
		},
		accounts.Membership{
			AccountID: viewerContext.CurrentAccount.ID,
			UserID:    viewerContext.User.ID,
			Role:      viewerContext.CurrentAccount.Role,
			Status:    "active",
		},
		isAdmin,
	)
	if !h.policy.Can(actor, authz.PermWorkspaceSettings) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "email must be verified before accessing billing",
			"code":  "email_verification_required",
		})
		return uuid.Nil, false
	}

	return viewerContext.CurrentAccount.ID, true
}

func (h *Handler) resolveViewerContext(w http.ResponseWriter, r *http.Request) (accounts.ViewerContext, bool) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return accounts.ViewerContext{}, false
	}

	viewerContext, err := h.accountsSvc.EnsureViewerContext(r.Context(), viewer, parseAccountHeader(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return accounts.ViewerContext{}, false
	}

	return viewerContext, true
}

func writeProfileError(w http.ResponseWriter, err error) {
	var validationErr *ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": validationErr.Error()})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "profile not found"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
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

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
