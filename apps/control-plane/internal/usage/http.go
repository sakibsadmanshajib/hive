package usage

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
)

type Handler struct {
	svc         *Service
	accountsSvc *accounts.Service
}

func NewHandler(svc *Service, accountsSvc *accounts.Service) *Handler {
	return &Handler{svc: svc, accountsSvc: accountsSvc}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/current/request-attempts":
		h.handleListAttempts(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/current/usage-events":
		h.handleListEvents(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/internal/usage/attempts":
		h.handleInternalStartAttempt(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/internal/usage/events":
		h.handleInternalRecordEvent(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *Handler) handleListAttempts(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	limit, ok := parseLimit(w, r)
	if !ok {
		return
	}

	attempts, err := h.svc.ListAttempts(r.Context(), accountID, r.URL.Query().Get("request_id"), limit)
	if err != nil {
		writeUsageError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"attempts": attempts})
}

func (h *Handler) handleListEvents(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	limit, ok := parseLimit(w, r)
	if !ok {
		return
	}

	events, err := h.svc.ListEvents(r.Context(), ListEventsFilter{
		AccountID: accountID,
		RequestID: r.URL.Query().Get("request_id"),
		Limit:     limit,
	})
	if err != nil {
		writeUsageError(w, err)
		return
	}

	response := make([]map[string]any, 0, len(events))
	for _, event := range events {
		item := map[string]any{
			"request_id":        event.RequestID,
			"event_type":        event.EventType,
			"endpoint":          event.Endpoint,
			"model_alias":       event.ModelAlias,
			"status":            event.Status,
			"input_tokens":      event.InputTokens,
			"output_tokens":     event.OutputTokens,
			"hive_credit_delta": event.HiveCreditDelta,
			"customer_tags":     event.CustomerTags,
			"error_code":        event.ErrorCode,
			"error_type":        event.ErrorType,
			"created_at":        event.CreatedAt,
		}
		if event.CacheReadTokens > 0 {
			item["cache_read_tokens"] = event.CacheReadTokens
		}
		if event.CacheWriteTokens > 0 {
			item["cache_write_tokens"] = event.CacheWriteTokens
		}
		if event.APIKeyID != nil {
			item["api_key_id"] = event.APIKeyID.String()
		}
		response = append(response, item)
	}

	writeJSON(w, http.StatusOK, map[string]any{"events": response})
}

type internalStartAttemptRequest struct {
	AccountID     string         `json:"account_id"`
	RequestID     string         `json:"request_id"`
	AttemptNumber int            `json:"attempt_number"`
	Endpoint      string         `json:"endpoint"`
	ModelAlias    string         `json:"model_alias"`
	Status        AttemptStatus  `json:"status"`
	APIKeyID      string         `json:"api_key_id"`
	CustomerTags  map[string]any `json:"customer_tags"`
}

type internalRecordEventRequest struct {
	AccountID        string         `json:"account_id"`
	RequestAttemptID string         `json:"request_attempt_id"`
	APIKeyID         string         `json:"api_key_id"`
	RequestID        string         `json:"request_id"`
	EventType        UsageEventType `json:"event_type"`
	Endpoint         string         `json:"endpoint"`
	ModelAlias       string         `json:"model_alias"`
	Status           string         `json:"status"`
	InputTokens      int64          `json:"input_tokens"`
	OutputTokens     int64          `json:"output_tokens"`
	CacheReadTokens  int64          `json:"cache_read_tokens"`
	CacheWriteTokens int64          `json:"cache_write_tokens"`
	HiveCreditDelta  int64          `json:"hive_credit_delta"`
	CustomerTags     map[string]any `json:"customer_tags"`
	ErrorCode        string         `json:"error_code"`
	ErrorType        string         `json:"error_type"`
}

func (h *Handler) handleInternalStartAttempt(w http.ResponseWriter, r *http.Request) {
	var req internalStartAttemptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	accountID, err := parseInternalUUIDField(req.AccountID, "account_id")
	if err != nil {
		writeUsageError(w, err)
		return
	}

	apiKeyID, err := parseInternalOptionalUUIDPointerField(req.APIKeyID, "api_key_id")
	if err != nil {
		writeUsageError(w, err)
		return
	}

	attempt, err := h.svc.StartAttempt(r.Context(), StartAttemptInput{
		AccountID:     accountID,
		RequestID:     req.RequestID,
		AttemptNumber: req.AttemptNumber,
		Endpoint:      req.Endpoint,
		ModelAlias:    req.ModelAlias,
		Status:        req.Status,
		APIKeyID:      apiKeyID,
		CustomerTags:  req.CustomerTags,
	})
	if err != nil {
		writeUsageError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, attempt)
}

func (h *Handler) handleInternalRecordEvent(w http.ResponseWriter, r *http.Request) {
	var req internalRecordEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	accountID, err := parseInternalUUIDField(req.AccountID, "account_id")
	if err != nil {
		writeUsageError(w, err)
		return
	}

	requestAttemptID, err := parseInternalUUIDField(req.RequestAttemptID, "request_attempt_id")
	if err != nil {
		writeUsageError(w, err)
		return
	}

	apiKeyID, err := parseInternalOptionalUUIDPointerField(req.APIKeyID, "api_key_id")
	if err != nil {
		writeUsageError(w, err)
		return
	}

	event, err := h.svc.RecordEvent(r.Context(), RecordEventInput{
		AccountID:        accountID,
		RequestAttemptID: requestAttemptID,
		APIKeyID:         apiKeyID,
		RequestID:        req.RequestID,
		EventType:        req.EventType,
		Endpoint:         req.Endpoint,
		ModelAlias:       req.ModelAlias,
		Status:           req.Status,
		InputTokens:      req.InputTokens,
		OutputTokens:     req.OutputTokens,
		CacheReadTokens:  req.CacheReadTokens,
		CacheWriteTokens: req.CacheWriteTokens,
		HiveCreditDelta:  req.HiveCreditDelta,
		CustomerTags:     req.CustomerTags,
		ErrorCode:        req.ErrorCode,
		ErrorType:        req.ErrorType,
	})
	if err != nil {
		writeUsageError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, event)
}

func parseInternalUUIDField(value, field string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return uuid.Nil, &ValidationError{Field: field, Message: field + " must be a valid UUID"}
	}
	return parsed, nil
}

func parseInternalOptionalUUIDPointerField(value, field string) (*uuid.UUID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := parseInternalUUIDField(value, field)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
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

func parseLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit := 20
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be a positive integer"})
			return 0, false
		}
		limit = parsed
	}

	return limit, true
}

func writeUsageError(w http.ResponseWriter, err error) {
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

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
