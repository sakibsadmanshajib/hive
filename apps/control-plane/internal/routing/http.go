package routing

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

type selectRouteRequest struct {
	AliasID             string   `json:"alias_id"`
	NeedResponses       bool     `json:"need_responses"`
	NeedChatCompletions bool     `json:"need_chat_completions"`
	NeedEmbeddings      bool     `json:"need_embeddings"`
	NeedStreaming       bool     `json:"need_streaming"`
	NeedReasoning       bool     `json:"need_reasoning"`
	NeedCacheRead       bool     `json:"need_cache_read"`
	NeedCacheWrite      bool     `json:"need_cache_write"`
	AllowedAliases      []string `json:"allowed_aliases"`
	AllowedProviders    []string `json:"allowed_providers"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/internal/routing/select":
		h.handleSelectRoute(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *Handler) handleSelectRoute(w http.ResponseWriter, r *http.Request) {
	var request selectRouteRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	request.AliasID = strings.TrimSpace(request.AliasID)
	if request.AliasID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "alias_id is required"})
		return
	}

	result, err := h.svc.SelectRoute(r.Context(), SelectionInput{
		AliasID:             request.AliasID,
		NeedResponses:       request.NeedResponses,
		NeedChatCompletions: request.NeedChatCompletions,
		NeedEmbeddings:      request.NeedEmbeddings,
		NeedStreaming:       request.NeedStreaming,
		NeedReasoning:       request.NeedReasoning,
		NeedCacheRead:       request.NeedCacheRead,
		NeedCacheWrite:      request.NeedCacheWrite,
		AllowedAliases:      request.AllowedAliases,
		AllowedProviders:    request.AllowedProviders,
	})
	if err != nil {
		writeRoutingError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func writeRoutingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrAliasNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrRouteNotEligible):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
