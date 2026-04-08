package accounting

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
)

type Handler struct {
	svc         *Service
	accountsSvc *accounts.Service
}

type createReservationRequest struct {
	RequestID        string         `json:"request_id"`
	AttemptNumber    int            `json:"attempt_number"`
	APIKeyID         string         `json:"api_key_id"`
	Endpoint         string         `json:"endpoint"`
	ModelAlias       string         `json:"model_alias"`
	EstimatedCredits int64          `json:"estimated_credits"`
	PolicyMode       PolicyMode     `json:"policy_mode"`
	CustomerTags     map[string]any `json:"customer_tags"`
}

type expandReservationRequest struct {
	ReservationID     string `json:"reservation_id"`
	AdditionalCredits int64  `json:"additional_credits"`
}

type finalizeReservationRequest struct {
	ReservationID          string `json:"reservation_id"`
	ActualCredits          int64  `json:"actual_credits"`
	TerminalUsageConfirmed bool   `json:"terminal_usage_confirmed"`
	Status                 string `json:"status"`
}

type releaseReservationRequest struct {
	ReservationID string `json:"reservation_id"`
	Reason        string `json:"reason"`
}

func NewHandler(svc *Service, accountsSvc *accounts.Service) *Handler {
	return &Handler{svc: svc, accountsSvc: accountsSvc}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/accounts/current/credits/reservations":
		h.handleCreateReservation(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/accounts/current/credits/reservations/expand":
		h.handleExpandReservation(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/accounts/current/credits/reservations/finalize":
		h.handleFinalizeReservation(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/accounts/current/credits/reservations/release":
		h.handleReleaseReservation(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/internal/accounting/reservations":
		h.handleInternalCreateReservation(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/internal/accounting/reservations/finalize":
		h.handleInternalFinalizeReservation(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/internal/accounting/reservations/release":
		h.handleInternalReleaseReservation(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *Handler) handleCreateReservation(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	var req createReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	apiKeyID, err := parseOptionalUUIDPointerField(req.APIKeyID, "api_key_id")
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	reservation, err := h.svc.CreateReservation(r.Context(), CreateReservationInput{
		AccountID:        accountID,
		RequestID:        req.RequestID,
		AttemptNumber:    req.AttemptNumber,
		APIKeyID:         apiKeyID,
		Endpoint:         req.Endpoint,
		ModelAlias:       req.ModelAlias,
		EstimatedCredits: req.EstimatedCredits,
		PolicyMode:       req.PolicyMode,
		CustomerTags:     req.CustomerTags,
	})
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, reservation)
}

func (h *Handler) handleExpandReservation(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	var req expandReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	reservationID, err := parseUUIDField(req.ReservationID, "reservation_id")
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	reservation, err := h.svc.ExpandReservation(r.Context(), ExpandReservationInput{
		AccountID:         accountID,
		ReservationID:     reservationID,
		AdditionalCredits: req.AdditionalCredits,
	})
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, reservation)
}

func (h *Handler) handleFinalizeReservation(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	var req finalizeReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	reservationID, err := parseOptionalUUIDField(req.ReservationID)
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	reservation, err := h.svc.FinalizeReservation(r.Context(), FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservationID,
		ActualCredits:          req.ActualCredits,
		TerminalUsageConfirmed: req.TerminalUsageConfirmed,
		Status:                 req.Status,
	})
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, reservation)
}

func (h *Handler) handleReleaseReservation(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	var req releaseReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	reservationID, err := parseOptionalUUIDField(req.ReservationID)
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	reservation, err := h.svc.ReleaseReservation(r.Context(), ReleaseReservationInput{
		AccountID:     accountID,
		ReservationID: reservationID,
		Reason:        req.Reason,
	})
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, reservation)
}

type internalCreateReservationRequest struct {
	AccountID        string         `json:"account_id"`
	RequestID        string         `json:"request_id"`
	AttemptNumber    int            `json:"attempt_number"`
	APIKeyID         string         `json:"api_key_id"`
	Endpoint         string         `json:"endpoint"`
	ModelAlias       string         `json:"model_alias"`
	EstimatedCredits int64          `json:"estimated_credits"`
	PolicyMode       PolicyMode     `json:"policy_mode"`
	CustomerTags     map[string]any `json:"customer_tags"`
}

type internalFinalizeReservationRequest struct {
	AccountID              string `json:"account_id"`
	ReservationID          string `json:"reservation_id"`
	ActualCredits          int64  `json:"actual_credits"`
	TerminalUsageConfirmed bool   `json:"terminal_usage_confirmed"`
	Status                 string `json:"status"`
}

type internalReleaseReservationRequest struct {
	AccountID     string `json:"account_id"`
	ReservationID string `json:"reservation_id"`
	Reason        string `json:"reason"`
}

func (h *Handler) handleInternalCreateReservation(w http.ResponseWriter, r *http.Request) {
	var req internalCreateReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	accountID, err := parseUUIDField(req.AccountID, "account_id")
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	apiKeyID, err := parseOptionalUUIDPointerField(req.APIKeyID, "api_key_id")
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	reservation, err := h.svc.CreateReservation(r.Context(), CreateReservationInput{
		AccountID:        accountID,
		RequestID:        req.RequestID,
		AttemptNumber:    req.AttemptNumber,
		APIKeyID:         apiKeyID,
		Endpoint:         req.Endpoint,
		ModelAlias:       req.ModelAlias,
		EstimatedCredits: req.EstimatedCredits,
		PolicyMode:       req.PolicyMode,
		CustomerTags:     req.CustomerTags,
	})
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, reservation)
}

func (h *Handler) handleInternalFinalizeReservation(w http.ResponseWriter, r *http.Request) {
	var req internalFinalizeReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	accountID, err := parseUUIDField(req.AccountID, "account_id")
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	reservationID, err := parseOptionalUUIDField(req.ReservationID)
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	reservation, err := h.svc.FinalizeReservation(r.Context(), FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservationID,
		ActualCredits:          req.ActualCredits,
		TerminalUsageConfirmed: req.TerminalUsageConfirmed,
		Status:                 req.Status,
	})
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, reservation)
}

func (h *Handler) handleInternalReleaseReservation(w http.ResponseWriter, r *http.Request) {
	var req internalReleaseReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	accountID, err := parseUUIDField(req.AccountID, "account_id")
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	reservationID, err := parseOptionalUUIDField(req.ReservationID)
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	reservation, err := h.svc.ReleaseReservation(r.Context(), ReleaseReservationInput{
		AccountID:     accountID,
		ReservationID: reservationID,
		Reason:        req.Reason,
	})
	if err != nil {
		writeAccountingError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, reservation)
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

	return viewerContext.CurrentAccount.ID, true
}

func writeAccountingError(w http.ResponseWriter, err error) {
	var validationErr *ValidationError
	var policyErr *PolicyError

	switch {
	case errors.As(err, &validationErr):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": validationErr.Error()})
	case errors.As(err, &policyErr):
		writeJSON(w, http.StatusConflict, map[string]string{"error": policyErr.Error()})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "reservation not found"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func parseOptionalUUIDField(value string) (uuid.UUID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return uuid.Nil, nil
	}
	return parseUUIDField(value, "reservation_id")
}

func parseOptionalUUIDPointerField(value, field string) (*uuid.UUID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := parseUUIDField(value, field)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseUUIDField(value, field string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return uuid.Nil, &ValidationError{Field: field, Message: field + " must be a valid UUID"}
	}
	return parsed, nil
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
