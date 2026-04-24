package budgets

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
)

// Handler is the HTTP handler for budget threshold endpoints.
type Handler struct {
	svc         *Service
	accountsSvc *accounts.Service
}

// NewHandler creates a new budget HTTP handler.
func NewHandler(svc *Service, accountsSvc *accounts.Service) *Handler {
	return &Handler{svc: svc, accountsSvc: accountsSvc}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/current/budget":
		h.handleGetBudget(w, r)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/accounts/current/budget":
		h.handleUpsertBudget(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/accounts/current/budget/dismiss":
		h.handleDismissAlert(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *Handler) handleGetBudget(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	threshold, err := h.svc.GetThreshold(r.Context(), accountID)
	if err != nil {
		writeBudgetError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"threshold": threshold})
}

func (h *Handler) handleUpsertBudget(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	var input UpsertThresholdInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	threshold, err := h.svc.UpsertThreshold(r.Context(), accountID, input)
	if err != nil {
		writeBudgetError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, threshold)
}

func (h *Handler) handleDismissAlert(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	if err := h.svc.DismissAlert(r.Context(), accountID); err != nil {
		writeBudgetError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "dismissed"})
}

func (h *Handler) resolveCurrentAccountID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return uuid.Nil, false
	}

	viewerContext, err := h.accountsSvc.EnsureViewerContext(r.Context(), viewer, parseAccountHeader(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return uuid.Nil, false
	}

	if !viewerContext.User.EmailVerified {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "email must be verified before accessing billing",
			"code":  "email_verification_required",
		})
		return uuid.Nil, false
	}

	return viewerContext.CurrentAccount.ID, true
}

func writeBudgetError(w http.ResponseWriter, err error) {
	var validationErr *ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": validationErr.Error()})
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

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
