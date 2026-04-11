package http

import (
	"encoding/json"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/hivegpt/hive/apps/control-plane/internal/accounting"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/apikeys"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
	"github.com/hivegpt/hive/apps/control-plane/internal/catalog"
	"github.com/hivegpt/hive/apps/control-plane/internal/ledger"
	"github.com/hivegpt/hive/apps/control-plane/internal/payments"
	"github.com/hivegpt/hive/apps/control-plane/internal/platform/metrics"
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
	APIKeysHandler    *apikeys.Handler
	CatalogHandler    *catalog.Handler
	LedgerHandler     *ledger.Handler
	PaymentsHandler   *payments.Handler
	ProfilesHandler   *profiles.Handler
	RoutingHandler    *routing.Handler
	UsageHandler      *usage.Handler

	// MetricsRegistry provides Prometheus counters/histograms for HTTP instrumentation.
	// When non-nil, all requests are counted and timed via InstrumentHandler middleware.
	MetricsRegistry *metrics.Registry

	// PrometheusRegistry is the custom prometheus.Registry used to serve /metrics.
	// When non-nil, the /metrics endpoint is registered on the mux.
	PrometheusRegistry *prometheus.Registry

	// Mux is an optional pre-created *http.ServeMux. When provided, routes are
	// registered on it (enabling callers to add routes after NewRouter returns).
	// When nil, a new ServeMux is created internally.
	Mux *http.ServeMux
}

// NewRouter returns a configured http.Handler with all platform routes registered.
// If MetricsRegistry is set, all requests are wrapped with Prometheus instrumentation.
// If PrometheusRegistry is set, a /metrics endpoint is registered on the mux.
//
// IMPORTANT: The return type is http.Handler (not *http.ServeMux) so that the
// instrumentation wrapper can be applied transparently. Plan 01 (Wave 2) depends
// on this signature.
func NewRouter(cfg RouterConfig) http.Handler {
	mux := cfg.Mux
	if mux == nil {
		mux = http.NewServeMux()
	}

	mux.HandleFunc("/health", handleHealth)

	// Register /metrics endpoint using the custom prometheus registry (not DefaultRegistry).
	if cfg.PrometheusRegistry != nil {
		mux.Handle("/metrics", promhttp.HandlerFor(cfg.PrometheusRegistry, promhttp.HandlerOpts{}))
	}

	if cfg.CatalogHandler != nil {
		mux.Handle("/internal/catalog/snapshot", cfg.CatalogHandler)
	}

	if cfg.RoutingHandler != nil {
		mux.Handle("/internal/routing/select", cfg.RoutingHandler)
	}

	if cfg.AccountingHandler != nil {
		mux.Handle("/internal/accounting/reservations", cfg.AccountingHandler)
		mux.Handle("/internal/accounting/reservations/finalize", cfg.AccountingHandler)
		mux.Handle("/internal/accounting/reservations/release", cfg.AccountingHandler)
	}

	if cfg.UsageHandler != nil {
		mux.Handle("/internal/usage/attempts", cfg.UsageHandler)
		mux.Handle("/internal/usage/events", cfg.UsageHandler)
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

	if cfg.APIKeysHandler != nil && cfg.AuthMiddleware != nil {
		protectedAPIKeys := cfg.AuthMiddleware.Require(cfg.APIKeysHandler)
		mux.Handle("/api/v1/accounts/current/api-keys", protectedAPIKeys)
		mux.Handle("/api/v1/accounts/current/api-keys/", protectedAPIKeys)
		// Internal service-to-service route — no auth middleware.
		mux.Handle("/internal/apikeys/resolve", cfg.APIKeysHandler)
	}

	if cfg.AccountsHandler != nil && cfg.AuthMiddleware != nil {
		protected := cfg.AuthMiddleware.Require(cfg.AccountsHandler)
		mux.Handle("/api/v1/", protected)
	}

	// Authenticated checkout routes — payment provider requires logged-in user.
	if cfg.PaymentsHandler != nil && cfg.AuthMiddleware != nil {
		protectedPayments := cfg.AuthMiddleware.Require(cfg.PaymentsHandler)
		mux.Handle("/api/v1/accounts/current/checkout/rails", protectedPayments)
		mux.Handle("/api/v1/accounts/current/checkout/initiate", protectedPayments)
	}

	// Unauthenticated webhook routes — payment providers send server-to-server callbacks
	// without Hive auth tokens. Signature verification happens inside each rail's ProcessEvent.
	if cfg.PaymentsHandler != nil {
		mux.Handle("/webhooks/stripe", cfg.PaymentsHandler)
		mux.Handle("/webhooks/bkash/callback", cfg.PaymentsHandler)
		mux.Handle("/webhooks/sslcommerz/ipn", cfg.PaymentsHandler)
		mux.Handle("/webhooks/sslcommerz/success", cfg.PaymentsHandler)
		mux.Handle("/webhooks/sslcommerz/fail", cfg.PaymentsHandler)
		mux.Handle("/webhooks/sslcommerz/cancel", cfg.PaymentsHandler)
	}

	// Wrap the mux with Prometheus HTTP instrumentation if a metrics registry is provided.
	// /metrics itself is excluded from recording to avoid self-referential noise.
	if cfg.MetricsRegistry != nil {
		return metrics.InstrumentHandler(cfg.MetricsRegistry, mux)
	}
	return mux
}

// handleHealth responds with {"status":"ok"} for liveness probes.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok"})
}
