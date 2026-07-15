package licensing

import (
	"encoding/json"
	"net/http"
)

// Handler serves GET /internal/license/entitlement -- the one runtime read
// path every caller uses regardless of deployment mode. See router wiring
// in apps/control-plane/cmd/server/main.go: Source is a FileSource in Hive
// Enterprise and a CloudSource in Hive Cloud, and both satisfy the same
// interface, so this handler never branches on mode.
type Handler struct {
	Source   Source
	Recorder Recorder // optional; nil skips persistence (e.g. no DB pool)
}

// NewHandler constructs a Handler. source must not be nil; recorder may be
// nil to skip DB persistence.
func NewHandler(source Source, recorder Recorder) *Handler {
	return &Handler{Source: source, Recorder: recorder}
}

// ServeHTTP handles GET /internal/license/entitlement.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx := r.Context()
	e, err := h.Source.Current(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "license entitlement unavailable")
		return
	}
	if h.Recorder != nil {
		// Best-effort: a DB write failure never blocks an entitlement read.
		_ = h.Recorder.Record(ctx, e)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(e)
}

// writeError emits a JSON error body with a matching Content-Type header,
// matching the provider-blind JSON error pattern used elsewhere in
// control-plane (e.g. internal/featuregate/handler.go).
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: message})
}
