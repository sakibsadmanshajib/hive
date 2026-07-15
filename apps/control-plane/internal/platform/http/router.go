package http

import (
	"encoding/json"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounting"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/apikeys"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/budgets"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/egress"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/identity"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/metrics"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/profiles"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/routing"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usage"
)

// healthResponse is the JSON body returned by the /health endpoint.
type healthResponse struct {
	Status string `json:"status"`
}

// RouterConfig holds dependencies for building the HTTP router.
type RouterConfig struct {
	AuthMiddleware           *auth.Middleware
	AccountsHandler          *accounts.Handler
	AccountingHandler        *accounting.Handler
	APIKeysHandler           *apikeys.Handler
	CatalogHandler           *catalog.Handler
	CatalogVisibilityHandler *catalog.VisibilityHandler
	LedgerHandler            *ledger.Handler
	PaymentsHandler          *payments.Handler
	ProfilesHandler          *profiles.Handler
	RoutingHandler           *routing.Handler
	UsageHandler             *usage.Handler

	// IdentityHandler finalizes email verification for the authenticated caller
	// (issue #112). Registered under /api/v1 behind the auth middleware.
	IdentityHandler *identity.Handler

	// MetricsRegistry provides Prometheus counters/histograms for HTTP instrumentation.
	// When non-nil, all requests are counted and timed via InstrumentHandler middleware.
	MetricsRegistry *metrics.Registry

	// PrometheusRegistry is the custom prometheus.Registry used to serve /metrics.
	// When non-nil, the /metrics endpoint is registered on the mux.
	PrometheusRegistry *prometheus.Registry

	// BudgetsHandler handles budget threshold CRUD and alert dismissal endpoints.
	BudgetsHandler *budgets.Handler

	// Mux is an optional pre-created *http.ServeMux. When provided, routes are
	// registered on it (enabling callers to add routes after NewRouter returns).
	// When nil, a new ServeMux is created internally.
	Mux *http.ServeMux

	// InternalToken is the shared secret guarding the /internal/* service-to-service
	// routes (issue #108). When empty, those routes are left unauthenticated and the
	// control-plane logs a startup warning; when set, callers must present a matching
	// X-Internal-Token header.
	InternalToken string

	// ProvidersRouter exposes an InternalMux() for CRUD over custom_providers.
	// Mounted under /internal/providers (shared-secret) and
	// /api/v1/admin/providers (platform admin JWT).
	// Using a narrow interface avoids an import cycle between platform/http and providers.
	ProvidersRouter interface {
		InternalMux() http.Handler
	}

	// RoleSvc is required to gate the /api/v1/admin/providers routes
	// with RequirePlatformAdmin. When nil those admin routes are skipped.
	RoleSvc interface {
		RequirePlatformAdmin(http.Handler) http.Handler
	}

	// LiteLLMSyncHandler handles POST /internal/litellm/sync.
	// Guarded by the shared-secret token. When nil the route is not registered.
	LiteLLMSyncHandler http.Handler

	// FeatureGateHandler handles GET /internal/featuregate/{tenant_id}.
	// Guarded by the shared-secret token. When nil the route is not registered.
	FeatureGateHandler http.Handler

	// EgressPolicyHandler serves the egress-policy single source of truth
	// (issue #308): the owner-gated admin CRUD surface at
	// /api/v1/egress-policy/ and the shared-secret-guarded read surface at
	// /internal/egress-policy/. When nil neither route is registered.
	EgressPolicyHandler *egress.Handler
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

	// internal wraps a service-to-service handler with the shared-secret guard.
	internal := func(h http.Handler) http.Handler {
		return RequireInternalToken(cfg.InternalToken, h)
	}

	if cfg.CatalogHandler != nil {
		mux.Handle("/internal/catalog/snapshot", internal(cfg.CatalogHandler))
		// Public catalog endpoint — optional auth: if a valid bearer token is present
		// the Viewer (with TenantID from raw_user_meta_data.selected_tenant_id) is
		// stored in context so tenant-specific visibility filtering applies.
		// Unauthenticated callers receive only public/preview aliases.
		if cfg.AuthMiddleware != nil {
			mux.Handle("/api/v1/catalog/models", cfg.AuthMiddleware.OptionalRequire(cfg.CatalogHandler))
		} else {
			mux.Handle("/api/v1/catalog/models", cfg.CatalogHandler)
		}
	}

	// Phase 20 Plan 04 — tenant model visibility admin routes.
	// All /internal/catalog/visibility/* routes are guarded by the shared-secret token.
	if cfg.CatalogVisibilityHandler != nil {
		mux.Handle("/internal/catalog/visibility/", internal(cfg.CatalogVisibilityHandler.VisibilityMux()))
	}

	if cfg.RoutingHandler != nil {
		mux.Handle("/internal/routing/select", internal(cfg.RoutingHandler))
	}

	if cfg.AccountingHandler != nil {
		mux.Handle("/internal/accounting/reservations", internal(cfg.AccountingHandler))
		mux.Handle("/internal/accounting/reservations/finalize", internal(cfg.AccountingHandler))
		mux.Handle("/internal/accounting/reservations/release", internal(cfg.AccountingHandler))
	}

	if cfg.UsageHandler != nil {
		mux.Handle("/internal/usage/attempts", internal(cfg.UsageHandler))
		mux.Handle("/internal/usage/events", internal(cfg.UsageHandler))
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
		mux.Handle("/api/v1/accounts/current/invoices", protectedLedger)
		mux.Handle("/api/v1/accounts/current/invoices/", protectedLedger)
	}

	if cfg.UsageHandler != nil && cfg.AuthMiddleware != nil {
		protectedUsage := cfg.AuthMiddleware.Require(cfg.UsageHandler)
		mux.Handle("/api/v1/accounts/current/request-attempts", protectedUsage)
		mux.Handle("/api/v1/accounts/current/usage-events", protectedUsage)
		mux.Handle("/api/v1/accounts/current/analytics/usage", protectedUsage)
		mux.Handle("/api/v1/accounts/current/analytics/spend", protectedUsage)
		mux.Handle("/api/v1/accounts/current/analytics/errors", protectedUsage)
	}

	if cfg.BudgetsHandler != nil && cfg.AuthMiddleware != nil {
		protectedBudgets := cfg.AuthMiddleware.Require(cfg.BudgetsHandler)
		mux.Handle("/api/v1/accounts/current/budget", protectedBudgets)
		mux.Handle("/api/v1/accounts/current/budget/dismiss", protectedBudgets)
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
		// Internal service-to-service route — guarded by the shared-secret token.
		mux.Handle("/internal/apikeys/resolve", internal(cfg.APIKeysHandler))
	}

	// Authenticated email-verification finalize (issue #112). Registered before
	// the /api/v1/ catch-all; ServeMux longest-prefix match routes this exact
	// path here. The edge forwards only the user's session bearer.
	if cfg.IdentityHandler != nil && cfg.AuthMiddleware != nil {
		mux.Handle("/api/v1/accounts/current/email-verification/finalize",
			cfg.AuthMiddleware.Require(cfg.IdentityHandler))
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

	// Phase 20 Plan 03 — LiteLLM config sync endpoint.
	// POST /internal/litellm/sync triggers config regeneration and container restart.
	if cfg.LiteLLMSyncHandler != nil {
		mux.Handle("/internal/litellm/sync", internal(cfg.LiteLLMSyncHandler))
	}

	// Phase 20 Plan 02 — provider CRUD routes.
	// /internal/providers/* is guarded by the shared-secret token.
	// /api/v1/admin/providers/* is guarded by RequirePlatformAdmin (JWT path).
	if cfg.ProvidersRouter != nil {
		internalProviders := internal(cfg.ProvidersRouter.InternalMux())
		mux.Handle("/internal/providers", internalProviders)
		mux.Handle("/internal/providers/", internalProviders)

		if cfg.RoleSvc != nil && cfg.AuthMiddleware != nil {
			adminProviders := cfg.AuthMiddleware.Require(
				cfg.RoleSvc.RequirePlatformAdmin(cfg.ProvidersRouter.InternalMux()),
			)
			mux.Handle("/api/v1/admin/providers", adminProviders)
			mux.Handle("/api/v1/admin/providers/", adminProviders)
		}
	}

	// Issue #238 — per-tenant feature gate endpoint.
	// GET /internal/featuregate/{tenant_id} returns flags for edge-api gate middleware.
	if cfg.FeatureGateHandler != nil {
		mux.Handle("/internal/featuregate/", internal(cfg.FeatureGateHandler))
	}

	// Issue #308 — egress policy single source of truth. Admin CRUD is
	// owner-gated (auth middleware; the handler itself checks
	// IsWorkspaceOwner). The internal read surface is the single resolution
	// both the OpenHands allowed_hosts config and the desktop firewall rule
	// generator will consume (neither is wired yet).
	if cfg.EgressPolicyHandler != nil {
		mux.Handle("/internal/egress-policy/", internal(cfg.EgressPolicyHandler.InternalMux()))
		if cfg.AuthMiddleware != nil {
			mux.Handle("/api/v1/egress-policy/", cfg.AuthMiddleware.Require(cfg.EgressPolicyHandler.AdminMux()))
		}
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
