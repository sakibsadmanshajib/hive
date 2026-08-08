package http

import (
	"encoding/json"
	"net/http"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounting"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/apikeys"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/budgets"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/egress"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/identity"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/marketplace"
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

	// ProvidersRouter exposes the two CRUD surfaces over custom_providers:
	// InternalMux() is mounted under /internal/providers (shared-secret) and
	// AdminMux() under /api/v1/admin/providers (platform admin JWT). The two
	// are separate handlers because a ServeMux matches on the whole request
	// path, so one mux cannot serve both prefixes.
	// Using a narrow interface avoids an import cycle between platform/http and providers.
	ProvidersRouter interface {
		InternalMux() http.Handler
		AdminMux() http.Handler
	}

	// RoleSvc is required to gate the /api/v1/admin/providers routes
	// with RequirePlatformAdmin. When nil those admin routes are skipped.
	RoleSvc interface {
		RequirePlatformAdmin(http.Handler) http.Handler
	}

	// WorkspaceAdminGate gates the workspace-scoped admin surfaces (feature
	// gates and the marketplace) on the OWNER of the tenant in scope, admitting
	// a platform admin as well. Platform operations such as provider CRUD and
	// credit grants stay on RoleSvc.RequirePlatformAdmin. When nil those
	// workspace-scoped routes are skipped, rather than falling back to a wider
	// gate. See issue #758.
	WorkspaceAdminGate interface {
		Require(http.Handler) http.Handler
	}

	// LiteLLMSyncHandler handles POST /internal/litellm/sync.
	// Guarded by the shared-secret token. When nil the route is not registered.
	LiteLLMSyncHandler http.Handler

	// FeatureGateHandler handles GET /internal/featuregate/{tenant_id}.
	// Guarded by the shared-secret token. When nil the route is not registered.
	FeatureGateHandler http.Handler

	// FeatureGateAdminHandler exposes the owner-gated admin feature-gate CRUD
	// surface (issue #292, agent-subsystem blueprint Step 1.2): AdminMux()
	// routes GET/PUT under /api/v1/admin/feature-gates. Mounted behind
	// AuthMiddleware.Require + RoleSvc.RequirePlatformAdmin (JWT path). A narrow
	// interface avoids an import cycle between platform/http and featuregate.
	// When nil (or RoleSvc/AuthMiddleware nil) the routes are not registered.
	FeatureGateAdminHandler interface {
		AdminMux() http.Handler
	}

	// EgressPolicyHandler serves the egress-policy single source of truth
	// (issue #308): the owner-gated admin CRUD surface at
	// /api/v1/egress-policy/ and the shared-secret-guarded read surface at
	// /internal/egress-policy/. When nil neither route is registered.
	EgressPolicyHandler *egress.Handler

	// MarketplaceHandler serves the admin-curated MCP and skills marketplace
	// (issue #309, agent-subsystem blueprint Step 2.3): the owner-gated admin
	// CRUD + per-tenant enable/disable surface at /api/v1/admin/marketplace/
	// and the shared-secret-guarded read surface at /internal/marketplace/
	// that apps/agent-engine consumes. When nil neither route is registered.
	MarketplaceHandler *marketplace.Handler

	// AgentTaskHandler serves agent task persistence (issue #311, agent-
	// subsystem blueprint Step 3.4): the shared-secret-guarded
	// service-to-service surface at /internal/agent-tasks/ that edge-api's
	// customer-facing /v1/agent/tasks routes call into. When nil the route
	// is not registered.
	AgentTaskHandler *agenttask.Handler
}

// NewRouter returns a configured http.Handler with all platform routes registered.
// If MetricsRegistry is set, all requests are wrapped with Prometheus instrumentation.
// This router deliberately serves no /metrics endpoint: the Prometheus series
// carry provider names, payment-rail counts, and the full internal endpoint
// inventory, and this handler is the one exposed through the public ingress.
// The scrape endpoint lives on the separate telemetry listener started in
// cmd/server (metricsListenAddr), which is not published or routed publicly.
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

	// internal wraps a service-to-service handler with the shared-secret guard.
	internal := func(h http.Handler) http.Handler {
		return RequireInternalToken(cfg.InternalToken, h)
	}

	if cfg.CatalogHandler != nil {
		mux.Handle("/internal/catalog/snapshot", internal(cfg.CatalogHandler))
		// Tenant-scoped snapshot (/internal/catalog/snapshot/tenant/{tenantID}).
		// edge-api calls this for /v1/models so the list a tenant is shown is the
		// same set the tenant is entitled to invoke.
		mux.Handle("/internal/catalog/snapshot/", internal(cfg.CatalogHandler))
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
		// The Phase 14 workspace surface. budgets.Handler has always dispatched
		// these two prefixes, but they were never mounted here, so every
		// request fell through to the /api/v1/ catch-all and came back 404.
		// That made a hard spend cap and a spend alert impossible to save from
		// the console even though the handler and its own tests were correct.
		mux.Handle("/api/v1/budgets/", protectedBudgets)
		mux.Handle("/api/v1/spend-alerts/", protectedBudgets)
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
		// Read-only status of one of the caller's own payment intents. Backs the
		// browser return page (issue #538).
		mux.Handle("/api/v1/accounts/current/checkout/intent", protectedPayments)
	}

	// Unauthenticated webhook routes — payment providers send server-to-server callbacks
	// without Hive auth tokens. Signature verification happens inside each rail's ProcessEvent.
	//
	// SSLCommerz previously also had /webhooks/sslcommerz/success, /fail and
	// /cancel registered here. Those are browser return URLs, not webhooks: a
	// paying customer was redirected to them and got raw JSON back, and a
	// browser request could reach settlement. They are gone; browser returns now
	// land on the console and the IPN endpoint is the only settlement trigger.
	if cfg.PaymentsHandler != nil {
		mux.Handle("/webhooks/stripe", cfg.PaymentsHandler)
		mux.Handle("/webhooks/bkash/callback", cfg.PaymentsHandler)
		mux.Handle("/webhooks/sslcommerz/ipn", cfg.PaymentsHandler)
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
				cfg.RoleSvc.RequirePlatformAdmin(cfg.ProvidersRouter.AdminMux()),
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

	// Issue #292 (blueprint Step 1.2) — admin feature-gate CRUD, gated on the
	// workspace administrator (JWT path). GET lists the registry joined with the
	// tenant enablement; PUT toggles one gate for the selected tenant. Mirrors
	// providers CRUD stays platform-admin only, see the mount above.
	if cfg.FeatureGateAdminHandler != nil && cfg.WorkspaceAdminGate != nil && cfg.AuthMiddleware != nil {
		adminFeatureGates := cfg.AuthMiddleware.Require(
			cfg.WorkspaceAdminGate.Require(cfg.FeatureGateAdminHandler.AdminMux()),
		)
		mux.Handle("/api/v1/admin/feature-gates", adminFeatureGates)
		mux.Handle("/api/v1/admin/feature-gates/", adminFeatureGates)
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

	// Issue #309 (blueprint Step 2.3) — MCP and skills marketplace, admin-
	// curated baseline. The internal read surface is the seam
	// apps/agent-engine/internal/marketplaceclient consumes to build a
	// session MCP config. The admin surface is workspace-administrator gated,
	// mirroring /api/v1/admin/feature-gates, while catalog curation inside the
	// handler stays platform-admin only.
	if cfg.MarketplaceHandler != nil {
		mux.Handle("/internal/marketplace/", internal(cfg.MarketplaceHandler.InternalMux()))
		if cfg.WorkspaceAdminGate != nil && cfg.AuthMiddleware != nil {
			adminMarketplace := cfg.AuthMiddleware.Require(
				cfg.WorkspaceAdminGate.Require(cfg.MarketplaceHandler.AdminMux()),
			)
			mux.Handle("/api/v1/admin/marketplace", adminMarketplace)
			mux.Handle("/api/v1/admin/marketplace/", adminMarketplace)
		}
	}

	// Issue #311 (blueprint Step 3.4) — agent task persistence, web side.
	// Service-to-service only: edge-api's /v1/agent/tasks routes are the
	// customer-facing surface, this is the internal store they call into.
	if cfg.AgentTaskHandler != nil {
		mux.Handle("/internal/agent-tasks/", internal(cfg.AgentTaskHandler.InternalMux()))
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
