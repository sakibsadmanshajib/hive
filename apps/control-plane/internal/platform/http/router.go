package http

import (
	"encoding/json"
	"net/http"

	"github.com/hivegpt/hive/apps/control-plane/internal/accounting"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
	"github.com/hivegpt/hive/apps/control-plane/internal/catalog"
	"github.com/hivegpt/hive/apps/control-plane/internal/ledger"
	"github.com/hivegpt/hive/apps/control-plane/internal/profiles"
	"github.com/hivegpt/hive/apps/control-plane/internal/routing"
	"github.com/hivegpt/hive/apps/control-plane/internal/usage"
)

// healthResponse is the JSON body returned by the /health endpoint.
type healthResponse struct {
	Status string `json:"status"`
}

// RouterConfig holds dependencies for building the HTTP router.
type RouterConfig struct {
	AuthMiddleware    *auth.Middleware
	AccountsHandler   *accounts.Handler
	AccountingHandler *accounting.Handler
	CatalogHandler    *catalog.Handler
	LedgerHandler     *ledger.Handler
	ProfilesHandler   *profiles.Handler
	RoutingHandler    *routing.Handler
	UsageHandler      *usage.Handler
}

// NewRouter returns a configured http.ServeMux with all platform routes registered.
func NewRouter(cfg RouterConfig) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", handleHealth)

	if cfg.CatalogHandler != nil {
		mux.Handle("/internal/catalog/snapshot", cfg.CatalogHandler)
	}

	if cfg.RoutingHandler != nil {
		mux.Handle("/internal/routing/select", cfg.RoutingHandler)
	}

	if cfg.ProfilesHandler != nil && cfg.AuthMiddleware != nil {
		protectedProfiles := cfg.AuthMiddleware.Require(cfg.ProfilesHandler)
		mux.Handle("/api/v1/accounts/current/profile", protectedProfiles)
		mux.Handle("/api/v1/accounts/current/billing-profile", protectedProfiles)
	}

	if cfg.LedgerHandler != nil && cfg.AuthMiddleware != nil {
		protectedLedger := cfg.AuthMiddleware.Require(cfg.LedgerHandler)
		mux.Handle("/api/v1/accounts/current/credits/balance", protectedLedger)
		mux.Handle("/api/v1/accounts/current/credits/ledger", protectedLedger)
	}

	if cfg.UsageHandler != nil && cfg.AuthMiddleware != nil {
		protectedUsage := cfg.AuthMiddleware.Require(cfg.UsageHandler)
		mux.Handle("/api/v1/accounts/current/request-attempts", protectedUsage)
		mux.Handle("/api/v1/accounts/current/usage-events", protectedUsage)
	}

	if cfg.AccountingHandler != nil && cfg.AuthMiddleware != nil {
		protectedAccounting := cfg.AuthMiddleware.Require(cfg.AccountingHandler)
		mux.Handle("/api/v1/accounts/current/credits/reservations", protectedAccounting)
		mux.Handle("/api/v1/accounts/current/credits/reservations/expand", protectedAccounting)
		mux.Handle("/api/v1/accounts/current/credits/reservations/finalize", protectedAccounting)
		mux.Handle("/api/v1/accounts/current/credits/reservations/release", protectedAccounting)
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
