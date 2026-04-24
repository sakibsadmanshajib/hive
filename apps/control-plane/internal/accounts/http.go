package accounts

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
)

// Handler handles all accounts-related HTTP routes.
type Handler struct {
	svc *Service
}

// NewHandler returns a new accounts Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ServeHTTP dispatches requests to the appropriate sub-handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/viewer":
		h.handleGetViewer(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/current/members":
		h.handleListMembers(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/accounts/current/invitations":
		h.handleCreateInvitation(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/invitations/accept":
		h.handleAcceptInvitation(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

// handleGetViewer implements GET /api/v1/viewer
func (h *Handler) handleGetViewer(w http.ResponseWriter, r *http.Request) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	requestedAccountID := parseAccountHeader(r)

	vc, err := h.svc.EnsureViewerContext(r.Context(), viewer, requestedAccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, viewerContextResponse(vc))
}

// handleListMembers implements GET /api/v1/accounts/current/members
func (h *Handler) handleListMembers(w http.ResponseWriter, r *http.Request) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	requestedAccountID := parseAccountHeader(r)

	vc, err := h.svc.EnsureViewerContext(r.Context(), viewer, requestedAccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if !vc.User.EmailVerified {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "email must be verified before accessing members",
			"code":  "email_verification_required",
		})
		return
	}

	members, err := h.svc.ListMembers(r.Context(), vc.CurrentAccount.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	type memberItem struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
		Status string `json:"status"`
	}
	items := make([]memberItem, 0, len(members))
	for _, m := range members {
		items = append(items, memberItem{
			UserID: m.UserID.String(),
			Role:   m.Role,
			Status: m.Status,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"members": items})
}

// handleCreateInvitation implements POST /api/v1/accounts/current/invitations
func (h *Handler) handleCreateInvitation(w http.ResponseWriter, r *http.Request) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email is required"})
		return
	}

	requestedAccountID := parseAccountHeader(r)

	vc, err := h.svc.EnsureViewerContext(r.Context(), viewer, requestedAccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	result, err := h.svc.CreateInvitation(r.Context(), vc.CurrentAccount.ID, viewer, body.Email)
	if err != nil {
		var gateErr *GateError
		if AsGateError(err, &gateErr) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": gateErr.Message,
				"code":  gateErr.Code,
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":         result.ID.String(),
		"email":      result.Email,
		"token":      result.Token,
		"expires_at": result.ExpiresAt,
	})
}

// handleAcceptInvitation implements POST /api/v1/invitations/accept
func (h *Handler) handleAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token is required"})
		return
	}

	accountID, err := h.svc.AcceptInvitation(r.Context(), viewer, body.Token)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"account_id": accountID.String(),
	})
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

// viewerContextResponse converts a ViewerContext to the JSON response shape.
func viewerContextResponse(vc ViewerContext) map[string]interface{} {
	memberships := make([]map[string]interface{}, 0, len(vc.Memberships))
	for _, m := range vc.Memberships {
		memberships = append(memberships, map[string]interface{}{
			"account_id":   m.AccountID.String(),
			"display_name": m.DisplayName,
			"role":         m.Role,
			"status":       m.Status,
		})
	}

	return map[string]interface{}{
		"user": map[string]interface{}{
			"id":             vc.User.ID.String(),
			"email":          vc.User.Email,
			"email_verified": vc.User.EmailVerified,
		},
		"current_account": map[string]interface{}{
			"id":           vc.CurrentAccount.ID.String(),
			"display_name": vc.CurrentAccount.DisplayName,
			"account_type": vc.CurrentAccount.AccountType,
			"role":         vc.CurrentAccount.Role,
		},
		"memberships": memberships,
		"gates": map[string]interface{}{
			"can_invite_members": vc.Gates.CanInviteMembers,
			"can_manage_api_keys": vc.Gates.CanManageAPIKeys,
		},
	}
}
