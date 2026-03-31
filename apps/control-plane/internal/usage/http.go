package usage

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

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
		response = append(response, item)
	}

	writeJSON(w, http.StatusOK, map[string]any{"events": response})
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
