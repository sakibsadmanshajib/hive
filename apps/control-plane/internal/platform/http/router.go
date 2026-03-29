package http

import (
	"encoding/json"
	"net/http"

	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
	"github.com/hivegpt/hive/apps/control-plane/internal/profiles"
)

// healthResponse is the JSON body returned by the /health endpoint.
type healthResponse struct {
	Status string `json:"status"`
}

// RouterConfig holds dependencies for building the HTTP router.
type RouterConfig struct {
	AuthMiddleware  *auth.Middleware
	AccountsHandler *accounts.Handler
	ProfilesHandler *profiles.Handler
}

// NewRouter returns a configured http.ServeMux with all platform routes registered.
func NewRouter(cfg RouterConfig) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", handleHealth)

	if cfg.ProfilesHandler != nil && cfg.AuthMiddleware != nil {
		protectedProfiles := cfg.AuthMiddleware.Require(cfg.ProfilesHandler)
		mux.Handle("/api/v1/accounts/current/profile", protectedProfiles)
		mux.Handle("/api/v1/accounts/current/billing-profile", protectedProfiles)
	}

	if cfg.AccountsHandler != nil && cfg.AuthMiddleware != nil {
		protected := cfg.AuthMiddleware.Require(cfg.AccountsHandler)
		mux.Handle("/api/v1/", protected)
	}

	return mux
}

// handleHealth responds with {"status":"ok"} for liveness probes.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
}
