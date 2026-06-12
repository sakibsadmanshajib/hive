package providers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// Handler exposes CRUD endpoints for custom_providers.
type Handler struct {
	svc *Service
}

// NewHandler returns a Handler backed by the given service.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// InternalMux returns an http.Handler that routes the five CRUD endpoints.
// Callers should wrap this with RequireInternalToken before mounting.
//
//	POST   /internal/providers
//	GET    /internal/providers
//	GET    /internal/providers/{id}
//	PUT    /internal/providers/{id}
//	DELETE /internal/providers/{id}
func (h *Handler) InternalMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/providers", h.handleCollection)
	mux.HandleFunc("/internal/providers/", h.handleItem)
	return mux
}

// handleCollection routes POST (create) and GET (list).
func (h *Handler) handleCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.handleCreate(w, r)
	case http.MethodGet:
		h.handleList(w, r)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

// handleItem routes GET/PUT/DELETE for /internal/providers/{id}.
func (h *Handler) handleItem(w http.ResponseWriter, r *http.Request) {
	id, ok := extractID(w, r.URL.Path)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGet(w, r, id)
	case http.MethodPut:
		h.handleUpdate(w, r, id)
	case http.MethodDelete:
		h.handleDelete(w, r, id)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

// providerRequest is the JSON body for create and update operations.
type providerRequest struct {
	Slug          string `json:"slug"`
	DisplayName   string `json:"display_name"`
	BaseURL       string `json:"base_url"`
	APIKeyEnv     string `json:"api_key_env"`
	LiteLLMPrefix string `json:"litellm_prefix"`
	Enabled       bool   `json:"enabled"`
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req providerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}

	p := Provider{
		Slug:          req.Slug,
		DisplayName:   req.DisplayName,
		BaseURL:       req.BaseURL,
		APIKeyEnv:     req.APIKeyEnv,
		LiteLLMPrefix: req.LiteLLMPrefix,
		Enabled:       req.Enabled,
	}

	created, err := h.svc.Create(r.Context(), p)
	if err != nil {
		mapServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	providers, err := h.svc.List(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "failed to list providers")
		return
	}
	writeJSON(w, http.StatusOK, providers)
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	p, err := h.svc.Get(r.Context(), id)
	if err != nil {
		mapServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	var req providerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_body", "request body must be valid JSON")
		return
	}

	p := Provider{
		Slug:          req.Slug,
		DisplayName:   req.DisplayName,
		BaseURL:       req.BaseURL,
		APIKeyEnv:     req.APIKeyEnv,
		LiteLLMPrefix: req.LiteLLMPrefix,
		Enabled:       req.Enabled,
	}

	updated, err := h.svc.Update(r.Context(), id, p)
	if err != nil {
		mapServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	if err := h.svc.Delete(r.Context(), id); err != nil {
		mapServiceError(w, err)
		return
	}
	// Return the updated (disabled) record so callers can confirm enabled=false.
	p, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "soft delete succeeded but get failed")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// mapServiceError translates sentinel errors to HTTP status codes.
func mapServiceError(w http.ResponseWriter, err error) {
	var valErr *ErrValidation
	switch {
	case errors.As(err, &valErr):
		writeJSONError(w, http.StatusBadRequest, "validation_error", valErr.Error())
	case errors.Is(err, ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "not_found", "provider not found")
	case errors.Is(err, ErrSlugConflict):
		writeJSONError(w, http.StatusConflict, "slug_conflict", "a provider with that slug already exists")
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
	}
}

// extractID parses the UUID segment from a path like /internal/providers/{id}.
func extractID(w http.ResponseWriter, path string) (uuid.UUID, bool) {
	// Trim trailing slash then get the last segment.
	path = strings.TrimSuffix(path, "/")
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_path", "missing provider id")
		return uuid.Nil, false
	}
	seg := path[idx+1:]
	id, err := uuid.Parse(seg)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_id", "provider id must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
}

type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: message, Code: code})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
