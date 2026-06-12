package litellmconfig

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// SyncHandler exposes POST /internal/litellm/sync.
// It must be wrapped with RequireInternalToken by the router.
type SyncHandler struct {
	svc SyncRunner
}

// NewSyncHandler returns an http.Handler backed by the given SyncRunner.
func NewSyncHandler(svc SyncRunner) http.Handler {
	return &SyncHandler{svc: svc}
}

func (h *SyncHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorBody{
			Code:    "method_not_allowed",
			Message: "only POST is accepted",
		})
		return
	}

	if err := h.svc.Sync(r.Context()); err != nil {
		slog.Error("litellmconfig: sync handler: sync failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody{
			Code:    "sync_failed",
			Message: "litellm config sync failed",
		})
		return
	}

	w.WriteHeader(http.StatusOK)
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
