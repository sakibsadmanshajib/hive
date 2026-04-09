package inference

import (
	"net/http"

	apierrors "github.com/hivegpt/hive/apps/edge-api/internal/errors"
)

// Handler routes inference requests to the appropriate endpoint handler.
type Handler struct {
	orchestrator *Orchestrator
}

// NewHandler creates a new inference Handler.
func NewHandler(orchestrator *Orchestrator) *Handler {
	return &Handler{orchestrator: orchestrator}
}

// ServeHTTP dispatches to the correct endpoint based on URL path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apierrors.WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Method not allowed", nil)
		return
	}

	switch r.URL.Path {
	case "/v1/chat/completions":
		handleChatCompletions(h.orchestrator, w, r)
	case "/v1/completions":
		handleCompletions(h.orchestrator, w, r)
	case "/v1/responses":
		apierrors.WriteError(w, http.StatusNotImplemented, "api_error", "The responses endpoint is not yet available.", nil)
	case "/v1/embeddings":
		apierrors.WriteError(w, http.StatusNotImplemented, "api_error", "The embeddings endpoint is not yet available.", nil)
	default:
		apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error", "Unknown endpoint", nil)
	}
}
