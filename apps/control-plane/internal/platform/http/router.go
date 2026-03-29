package http

import (
	"encoding/json"
	"net/http"
)

// healthResponse is the JSON body returned by the /health endpoint.
type healthResponse struct {
	Status string `json:"status"`
}

// NewRouter returns a configured http.ServeMux with all platform routes registered.
func NewRouter() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", handleHealth)

	return mux
}

// handleHealth responds with {"status":"ok"} for liveness probes.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
}
