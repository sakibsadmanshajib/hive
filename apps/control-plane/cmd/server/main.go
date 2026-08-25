package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	goredis "github.com/redis/go-redis/v9"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/engineapi"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounting"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agentengine"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agentsched"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/apikeys"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auditarchive"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auditverifier"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auditworker"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auditworker/sinks"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/authz"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/batchstore"
	batchexecutor "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/batchstore/executor"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/budgets"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/egress"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/featuregate"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/filestore"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/grants"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/identity"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/licensing"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/litellmconfig"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/marketplace"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/owui"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
	bkashRail "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments/bkash"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments/invoices"
	sslcommerzRail "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments/sslcommerz"
	stripeRail "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments/stripe"
	paymentStub "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments/stub"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/config"
	platformdb "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/db"
	platformhttp "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/http"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/metrics"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/rcache"
	platformredis "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/redis"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/profiles"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/providers"
	cprag "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/rag"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/routing"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/signup"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/signupguard"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/sovereign"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/spendalerts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenant/settings"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenants"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usage"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usermemories"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/waldrainer"
	"github.com/sakibsadmanshajib/hive/packages/embedmodel"
	"github.com/sakibsadmanshajib/hive/packages/storage"
)

// metricsListenAddr is the address of the telemetry-only listener that serves
// /metrics, kept off the public listener so the Prometheus series stay internal.
const metricsListenAddr = ":9101"

// provisioningGaugeInterval is how often the signup provisioning failure gauge is
// refreshed from the reconciler. Shorter than the sweep interval so a scrape
// never reads a value a whole pass out of date.
const provisioningGaugeInterval = time.Minute

// resolveHotCacheTTL reads HIVE_HOT_CACHE_TTL_SECONDS, the TTL for the
// routing/catalog hot-path read cache. Empty selects rcache.DefaultTTL (30s);
// a present but invalid or out-of-range value logs a warning and falls back
// rather than failing startup over an optional tuning knob. The ceiling of
// 24h keeps a typo from turning the cache into a day-long staleness window.
func resolveHotCacheTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv("HIVE_HOT_CACHE_TTL_SECONDS"))
	if raw == "" {
		return rcache.DefaultTTL
	}
	n, err := strconv.Atoi(raw)
	const maxTTLSeconds = 24 * 60 * 60
	if err != nil || n <= 0 || n > maxTTLSeconds {
		log.Printf("WARNING: HIVE_HOT_CACHE_TTL_SECONDS=%q is not an integer in (0, %d]; using default %v", raw, maxTTLSeconds, rcache.DefaultTTL)
		return rcache.DefaultTTL
	}
	return time.Duration(n) * time.Second
}

// How long startup waits for the database before giving up, and how often it
// retries inside that window. The session-mode pooler is shared and capped at
// 15 clients across CI, developer stacks and the live deployment, so a boot can
// be refused for a few seconds while another consumer holds the last slot.
// The budget is sized to fit inside the compose healthcheck's 120s start_period
// with room for the rest of startup, so a database that is genuinely
// unreachable still fails the container instead of stretching the boot out.
const (
	dbOpenBudget        = 75 * time.Second
	dbOpenRetryInterval = 3 * time.Second
)

// ledgerGrantAdapter wraps *ledger.Service to satisfy the paymentStub.LedgerGranter
// interface (which returns only error, discarding the LedgerEntry return value).
// Used only when HIVE_PAYMENTS_STUB=true is set.
type ledgerGrantAdapter struct {
	svc *ledger.Service
}

func (a *ledgerGrantAdapter) GrantCredits(
	ctx context.Context,
	accountID uuid.UUID,
	idempotencyKey string,
	credits int64,
	metadata map[string]any,
) error {
	_, err := a.svc.GrantCredits(ctx, accountID, idempotencyKey, credits, metadata)
	return err
}

// stubCountryAdapter wraps *profiles.Service to satisfy the
// paymentStub.AccountCountryReader interface, exposing only the account's ISO
// country code so the stub can apply the same country to rail access control
// as the production payment service. Used only when HIVE_PAYMENTS_STUB=true.
type stubCountryAdapter struct {
	svc *profiles.Service
}

func (a *stubCountryAdapter) CountryCode(ctx context.Context, accountID uuid.UUID) (string, error) {
	profile, err := a.svc.GetAccountProfile(ctx, accountID)
	if err != nil {
		return "", err
	}
	return profile.CountryCode, nil
}

// accountsResolverAdapter adapts accounts.Service to the payments.AccountResolver interface.
// It extracts the viewer from context (set by auth middleware) and resolves the current account.
type accountsResolverAdapter struct {
	svc *accounts.Service
}

// =============================================================================
// Phase 14 invoices adapters — bridge accounts.Repository to the invoice
// service's narrow AccessChecker + WorkspaceNamer ports. Phase 18 RBAC will
// replace these with the tier-aware predicate layer.
// =============================================================================

type accountsAccessChecker struct{ repo accounts.Repository }

func newAccountsAccessChecker(repo accounts.Repository) invoices.AccessChecker {
	return &accountsAccessChecker{repo: repo}
}

// IsWorkspaceMember returns whether userID has any active membership row on
// the given workspace (account) id. Phase 14 = "any role"; Phase 18 may
// narrow.
//
// ListMembershipsByUserID returns invited rows alongside active ones (the
// console lists both), so the status check here is what makes the sentence
// above true: an invited-but-not-accepted seat must not read a workspace's
// invoices.
func (a *accountsAccessChecker) IsWorkspaceMember(ctx context.Context, userID, workspaceID uuid.UUID) (bool, error) {
	memberships, err := a.repo.ListMembershipsByUserID(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("invoices access: list memberships: %w", err)
	}
	for _, m := range memberships {
		if m.AccountID == workspaceID && m.Status == accounts.StatusActive {
			return true, nil
		}
	}
	return false, nil
}

type accountsNamer struct{ repo accounts.Repository }

func newAccountsNamer(repo accounts.Repository) invoices.WorkspaceNamer {
	return &accountsNamer{repo: repo}
}

// WorkspaceName resolves the human label printed in the invoice PDF header.
// Falls back to the UUID string when the row is missing or has no name.
func (a *accountsNamer) WorkspaceName(ctx context.Context, workspaceID uuid.UUID) (string, error) {
	acct, err := a.repo.GetAccountByID(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	if acct == nil || acct.DisplayName == "" {
		return workspaceID.String(), nil
	}
	return acct.DisplayName, nil
}

func (a *accountsResolverAdapter) EnsureViewerContext(ctx context.Context) (uuid.UUID, error) {
	viewer, ok := auth.ViewerFromContext(ctx)
	if !ok {
		return uuid.Nil, fmt.Errorf("payments: no authenticated viewer in context")
	}
	viewerCtx, err := a.svc.EnsureViewerContext(ctx, viewer, uuid.Nil)
	if err != nil {
		return uuid.Nil, err
	}
	if !viewerCtx.User.EmailVerified {
		return uuid.Nil, payments.ErrVerificationRequired
	}
	return viewerCtx.CurrentAccount.ID, nil
}

func main() {
	// Sovereign-mode guard: fail fast before any service wiring when external
	// provider keys are present. See apps/control-plane/internal/sovereign for tests.
	if err := sovereign.Check(os.Getenv); err != nil {
		log.Fatal(err)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	storageCfg, err := loadStorageConfigFromEnv()
	if err != nil {
		log.Fatalf("storage unavailable: %v", err)
	}
	storageClient, err := storage.NewS3Client(storageCfg.Client)
	if err != nil {
		log.Fatalf("storage unavailable: %v", err)
	}

	// Payment provider credentials (all optional — missing vars skip that rail).
	stripeSecretKey := os.Getenv("STRIPE_SECRET_KEY")
	stripeWebhookSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	bkashAppKey := os.Getenv("BKASH_APP_KEY")
	bkashAppSecret := os.Getenv("BKASH_APP_SECRET")
	bkashUsername := os.Getenv("BKASH_USERNAME")
	bkashPassword := os.Getenv("BKASH_PASSWORD")
	bkashBaseURL := os.Getenv("BKASH_BASE_URL")
	sslcommerzStoreID := os.Getenv("SSLCOMMERZ_STORE_ID")
	sslcommerzStorePasswd := os.Getenv("SSLCOMMERZ_STORE_PASSWD")
	sslcommerzBaseURL := os.Getenv("SSLCOMMERZ_BASE_URL")
	xeAccountID := os.Getenv("XE_ACCOUNT_ID")
	xeAPIKey := os.Getenv("XE_API_KEY")

	// Apply default sandbox base URLs when not explicitly configured.
	if bkashBaseURL == "" {
		bkashBaseURL = "https://tokenized.sandbox.bka.sh/v1.2.0-beta"
	}
	if sslcommerzBaseURL == "" {
		sslcommerzBaseURL = "https://sandbox.sslcommerz.com"
	}

	// runCtx is the process-lifetime context for background goroutines
	// (audit sink worker, WAL drainer, hash-chain verifier). It is cancelled
	// on shutdown so those goroutines unwind cleanly instead of being killed
	// mid-write, which would risk partial WAL flushes and orphan outbox rows.
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	// Open the database pool. A missing SUPABASE_DB_URL is treated as a
	// non-fatal warning at startup so the service can still respond to /health
	// in environments where the DB URL is not yet provisioned.
	//
	// The attempt is retried for dbOpenBudget rather than made once: every
	// route below is wired on whether this pool exists, and /health reports on
	// it, so losing a single race for the shared session-mode pooler would
	// otherwise strand the whole process in a degraded state it can never leave.
	pool, dbErr := platformdb.OpenWithRetry(context.Background(), cfg.SupabaseDBURL, dbOpenBudget, dbOpenRetryInterval)
	if dbErr != nil {
		log.Printf("WARNING: database not available at startup: %v", dbErr)
	} else {
		defer pool.Close()
		log.Println("database pool ready")
	}

	// resolveHealth tracks /internal/apikeys/resolve outcomes at runtime so
	// /health can report a pool that came up fine at boot and then started
	// failing checkouts under contention — `pool != nil` alone cannot see
	// that, because a pgxpool.Pool is never nil again once opened. See
	// platform/db.ResolveHealth and issue #836.
	resolveHealth := platformdb.NewResolveHealth()

	// Budget for the remaining startup probes (the redis ping below). Created
	// after the database open so a slow open cannot expire it in advance and
	// turn an available redis into a spurious "redis not available" warning.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Build auth client and middleware. The membership check is what makes
	// Viewer.TenantID trustworthy: it is derived from user-writable
	// user_metadata.selected_tenant_id, so without validation any caller could
	// name any tenant. See auth.Client.WithMembershipCheck.
	authClient := auth.NewClient(cfg.SupabaseURL, cfg.SupabaseAnonKey)
	if pool != nil {
		authClient = authClient.WithMembershipCheck(tenantMembershipCheck(pool))
	} else {
		log.Println("WARNING: no database pool; selected-tenant membership validation disabled, tenant-scoped routes will deny")
	}
	authMiddleware := auth.NewMiddleware(authClient)

	// Build accounts service and handler (requires DB; skip if pool unavailable).
	var accountsHandler *accounts.Handler
	var accountingHandler *accounting.Handler
	var apikeysHandler *apikeys.Handler
	var budgetsHandler *budgets.Handler
	var invoicesHandler *invoices.Handler
	var grantsHandler *grants.Handler
	var roleSvc *platform.RoleService
	var authzMW authz.Middleware // Phase 18: set after roleSvc+accountsSvc are ready
	var catalogHandler *catalog.Handler
	// Hoisted: the visibility admin handler is built after the phase-19 block
	// below, once it is known whether an OWUI client exists to sync to.
	var catalogSvc *catalog.Service
	var catalogVisibilityHandler *catalog.VisibilityHandler
	var providersHandler *providers.Handler
	var litellmSyncHandler http.Handler
	var ledgerHandler *ledger.Handler
	var profilesHandler *profiles.Handler
	var routingHandler *routing.Handler
	var usageHandler *usage.Handler
	var redisClient *goredis.Client
	// Hoisted so the payments wiring block below can reference them.
	var accountsSvc *accounts.Service
	var accountingSvc *accounting.Service
	var ledgerSvc *ledger.Service
	var profilesSvc *profiles.Service
	var routingSvc *routing.Service
	// Phase 19 Plan 02 — hoisted so the route-mount block below can wire
	// the signup webhook and tenant switch handlers after the router mux
	// exists. nil when the database pool failed to come up.
	var owuiClient *owui.Client
	var auditLogger *audit.Logger
	var auditWAL *audit.FileWALWriter
	var signupWebhook *signup.Webhook
	var signupViewerHandler *signup.ViewerHandler
	// signupReconciler is the sweep that provisions identities nothing else
	// reached. It also answers the health endpoint, so a build that stops
	// wiring it reports degraded instead of starting quietly (D-023).
	var signupReconciler *signup.Reconciler
	var tenantsHandler *tenants.Handler
	// Signup abuse-prevention (issue #116). The disposable-domain blocklist is
	// parsed once from an embedded file (no network), so it is available even
	// when the database pool failed to come up. The per-IP limiter and the
	// Turnstile verifier are wired below once redisClient is known.
	disposableBlocklist, blErr := signupguard.LoadDisposableBlocklist()
	if blErr != nil {
		log.Fatalf("signupguard: load disposable blocklist: %v", blErr)
	}
	log.Printf("signupguard: disposable-domain blocklist loaded (%d domains)", disposableBlocklist.Len())
	var signupPrecheck *signupguard.Handler
	if pool != nil {
		if cfg.RedisURL != "" {
			redisClient = platformredis.NewClient(cfg.RedisURL)
			if err := platformredis.Ping(ctx, redisClient); err != nil {
				log.Printf("WARNING: redis not available at startup: %v", err)
				_ = redisClient.Close()
				redisClient = nil
			} else {
				defer redisClient.Close()
				log.Println("redis client ready")
			}
		}

		accountsRepo := accounts.NewPgxRepository(pool)
		accountsSvc = accounts.NewService(accountsRepo)
		// Lets a fresh personal workspace's account_membership retry the
		// tenant_billing_accounts mapping right after it lands — the other
		// half of the creation-path race fix in signup.EnsureTenantBillingAccount.
		accountsSvc = accountsSvc.WithBillingPool(pool)
		accountsHandler = accounts.NewHandler(accountsSvc)

		// Hot-path read cache (Redis): wraps the repositories behind
		// /internal/routing/select and /v1/models so each request's catalog,
		// pricing and entitlement reads hit Redis instead of Postgres for one
		// TTL at a time. Enabled only when REDIS_URL is set AND reachable at
		// startup; otherwise both repos run uncached exactly as before. The
		// dedicated hot-path client carries tight timeouts so a hung Redis
		// costs milliseconds, and Flush at boot drops keys written by a
		// previous binary so a migration that repriced aliases between deploys
		// is never answered with pre-deploy prices. Money state (balances,
		// reservations, ledger entries, billing state) is never cached; see
		// rcache.Cache's boundary note.
		var hotCache *rcache.Cache
		switch hc, hcErr := rcache.New(cfg.RedisURL, "hivecp:v1", resolveHotCacheTTL()); {
		case cfg.RedisURL == "":
			log.Println("REDIS_URL not set: routing/catalog reads run uncached")
		case hcErr != nil:
			log.Printf("WARNING: hot-path read cache disabled, client build failed: %v", hcErr)
		default:
			if pingErr := hc.Ping(ctx); pingErr != nil {
				log.Printf("WARNING: hot-path read cache disabled, redis unreachable: %v", pingErr)
				_ = hc.Close()
				break
			}
			flushed, flushErr := hc.Flush(ctx)
			if flushErr != nil {
				log.Printf("WARNING: hot-path cache startup flush failed (%v); keys from the previous binary may serve up to one TTL", flushErr)
			}
			hotCache = hc
			defer hc.Close()
			log.Printf("redis hot-path read cache enabled for routing/catalog (startup flush removed %d keys)", flushed)
		}

		catalogRepo := catalog.NewCachedRepository(catalog.NewPgxRepository(pool), hotCache)
		catalogSvc = catalog.NewService(catalogRepo)
		catalogHandler = catalog.NewHandler(catalogSvc)

		providersRepo := providers.NewPgxRepository(pool)
		providersSvc := providers.NewService(providersRepo)
		providersHandler = providers.NewHandler(providersSvc)
		log.Println("providers module ready (Phase 20 Plan 02)")

		// Phase 20 Plan 03 — LiteLLM config sync handler.
		// LITELLM_CONFIG_PATH defaults to /etc/litellm/config.yaml (shared volume mount).
		// LITELLM_MASTER_KEY is the LiteLLM proxy admin key.
		// LITELLM_CONTAINER_NAME is read inside NewDefaultDockerRestarter (default: litellm).
		litellmConfigMode := strings.TrimSpace(os.Getenv("LITELLM_CONFIG_MODE"))
		litellmConfigPath := os.Getenv("LITELLM_CONFIG_PATH")
		if litellmConfigPath == "" {
			litellmConfigPath = "/etc/litellm/config.yaml"
		}
		litellmMasterKey := resolveLiteLLMMasterKey()
		switch litellmConfigMode {
		case "", "file":
			litellmRestarter := litellmconfig.NewDefaultDockerRestarter("")
			litellmSyncSvc := litellmconfig.NewSyncService(pool, litellmConfigPath, litellmMasterKey, litellmRestarter)
			litellmSyncHandler = litellmconfig.NewSyncHandler(litellmSyncSvc)
		case "db":
			log.Fatalf("LITELLM_CONFIG_MODE=db is documented but not yet implemented in control-plane startup")
		default:
			log.Fatalf("invalid LITELLM_CONFIG_MODE %q: supported values are file (default) and db (not yet implemented)", litellmConfigMode)
		}
		log.Println("litellm sync handler ready (Phase 20 Plan 03)")

		routingRepo := routing.NewCachedRepository(routing.NewPgxRepository(pool), hotCache)
		// catalogSvc is the per-tenant entitlement source: route selection and
		// the catalog listing resolve visibility through the same predicate, so
		// a tenant cannot invoke a model an admin hid from it.
		routingSvc = routing.NewService(routingRepo, catalogSvc)
		routingHandler = routing.NewHandler(routingSvc)

		ledgerRepo := ledger.NewPgxRepository(pool)
		ledgerSvc = ledger.NewService(ledgerRepo)
		ledgerHandler = ledger.NewHandler(ledgerSvc, accountsSvc)

		profilesRepo := profiles.NewPgxRepository(pool)
		profilesSvc = profiles.NewService(profilesRepo)
		profilesHandler = profiles.NewHandler(profilesSvc, accountsSvc)

		usageRepo := usage.NewPgxRepository(pool)
		usageSvc := usage.NewService(usageRepo)
		usageHandler = usage.NewHandler(usageSvc, accountsSvc)

		apikeysRepo := apikeys.NewPgxRepository(pool)
		apikeysSvc := apikeys.NewService(apikeysRepo, apikeys.NewRedisSnapshotCache(redisClient))
		apikeysHandler = apikeys.NewHandler(apikeysSvc, accountsSvc).WithResolveHealth(resolveHealth)

		accountingRepo := accounting.NewPgxRepository(pool)
		// Postgres advisory locker serializes the credit-reservation critical
		// section across all control-plane instances, preventing the TOCTOU
		// credit double-spend (issue #106). Single-instance in-process locking
		// is the NewService default; this upgrades it to be cross-process safe.
		accountingSvc = accounting.NewService(accountingRepo, ledgerSvc, usageSvc, apikeysSvc).
			WithAccountLocker(accounting.NewPgxAccountLocker(pool))
		accountingHandler = accounting.NewHandler(accountingSvc, accountsSvc)

		// Issue #616 — stranded-hold reaper. A finalize that fails loses its
		// charge and strands the hold in the same step, and reserved credits
		// are subtracted from available balance until something releases them,
		// so an account can end up refused service it has already paid for.
		//
		// This runs in process rather than as a pg_cron job for two reasons.
		// The release has to go through the accounting service to take the
		// per-account lock, post a real reservation_release entry under an
		// idempotency key, write the reservation event and unwind the API key
		// delta; a SQL job would have to reimplement all of that against an
		// append-only ledger. And a pg_cron schedule that silently fails to
		// exist looks exactly like a system with no leak, which is the failure
		// mode that let this sit unnoticed for days. A missing runner here is
		// visible in the startup log below.
		reaperEnabled := !strings.EqualFold(strings.TrimSpace(os.Getenv("HIVE_RESERVATION_REAPER_ENABLED")), "false")
		reaperTTL := parseDurationEnv("HIVE_RESERVATION_REAPER_TTL", accounting.ReaperDefaultTTL)
		reaperInterval := parseDurationEnv("HIVE_RESERVATION_REAPER_INTERVAL", 15*time.Minute)
		if reaperEnabled {
			reservationReaper := accounting.NewReaper(accountingRepo, accountingSvc, accounting.ReaperConfig{
				TTL:      reaperTTL,
				Interval: reaperInterval,
				Logger:   slog.Default(),
			})
			reservationReaper.Start(runCtx)
			defer reservationReaper.Stop()
			log.Printf("reservation reaper started (ttl=%s, interval=%s)", reaperTTL, reaperInterval)
		} else {
			log.Println("reservation reaper DISABLED by HIVE_RESERVATION_REAPER_ENABLED=false; stranded credit holds will not be released")
		}

		budgetsRepo := budgets.NewPgxRepository(pool)
		workspaceBudgetsRepo := budgets.NewWorkspacePgxRepository(pool)
		emailNotifier := budgets.NewLogNotifier(slog.Default())
		alertNotifier := budgets.NewCompositeNotifier(nil, slog.Default())
		budgetsSvc := budgets.NewServiceWithWorkspace(budgetsRepo, emailNotifier, workspaceBudgetsRepo, alertNotifier, redisClient)
		budgetsHandler = budgets.NewHandler(budgetsSvc, accountsSvc)

		// Phase 14 — spend-alert cron runner (50/80/100% thresholds, one-shot per period).
		alertEvaluator := budgets.NewCronEvaluator(workspaceBudgetsRepo, alertNotifier, slog.Default())
		alertRunner := spendalerts.NewRunner(alertEvaluator, spendalerts.Config{
			Interval: 60 * time.Second,
			Logger:   slog.Default(),
		})
		// M2: bind to runCtx so the cron stops cleanly on shutdown
		// instead of being orphaned with context.Background().
		alertRunner.Start(runCtx)
		defer alertRunner.Stop()
		log.Println("spend-alert cron runner started (interval=60s)")

		// Phase 14 — Invoices: monthly BDT-only invoice generator + cron.
		// Wires the new sub-package /internal/payments/invoices/. The cron
		// fires at 02:00 UTC on day 1 each month and produces one invoice per
		// active workspace covering the prior calendar month. Idempotent.
		invoicesRepo := invoices.NewPgxRepository(pool)
		invoicesAccess := newAccountsAccessChecker(accountsRepo)
		invoicesNamer := newAccountsNamer(accountsRepo)
		invoicesStorage := invoices.NewStorageAdapter(storageClient)
		invoicesSvc := invoices.NewService(
			invoicesRepo,
			invoicesStorage,
			invoices.NewGofpdfRenderer(),
			invoicesAccess,
			invoicesNamer,
			slog.Default(),
		)
		invoicesHandler = invoices.NewHandler(invoicesSvc)

		invoicesCron := invoices.NewCron(invoicesSvc, invoicesRepo, invoices.CronConfig{
			Logger:   slog.Default(),
			Interval: time.Hour,
		})
		// M2: bind to runCtx so the monthly invoice cron stops with
		// the server, not with context.Background().
		invoicesCron.Start(runCtx)
		defer invoicesCron.Stop()
		log.Println("invoice monthly cron started (window=day-1 02:00 UTC)")

		// Phase 14 — owner-discretionary credit grants. Same-tx ledger
		// append + immutable audit row (BEFORE UPDATE OR DELETE trigger
		// guards mutations at schema level). RoleService gates the admin
		// surface; the self-list surface uses plain auth middleware only.
		roleSvc = platform.NewRoleService(platform.NewPgxRoleStore(pool))

		// Phase 18 — authz middleware: resolves Actor from request context and
		// enforces Permission-level gates. Constructed once and shared by all
		// handler wiring below that needs permission checks.
		actorResolver := accounts.NewActorResolver(accountsSvc, roleSvc)
		authzMW = authz.NewMiddleware(actorResolver)

		// Phase 14 — wire role service into the budgets handler so owner-gated
		// workspace routes (PUT/DELETE /api/v1/budgets/{ws}, /api/v1/spend-alerts)
		// can call IsWorkspaceOwner. Without this the customer-facing surface
		// returns 503 "role service unavailable" on every mutating request.
		budgetsHandler = budgetsHandler.WithRoleService(roleSvc)

		// Wire role service into the accounts handler so GET /api/v1/viewer
		// reports the real platform-admin overlay in permissions[]. Without
		// this, platform admins never see platform.admin there and the
		// web-console Feature Gates/Marketplace pages refuse to render even
		// though the underlying admin-gated routes already allow them.
		accountsHandler = accountsHandler.WithRoleService(roleSvc)

		// Phase 18 — wire role service into the apikeys handler so the admin
		// overlay is reflected in Actor.IsAdmin during PermAPIKeysWrite checks.
		// Without it, platform admins are silently denied by policy.Can.
		if apikeysHandler != nil {
			apikeysHandler = apikeysHandler.WithRoleService(roleSvc)
		}

		// Issue #424 — wire role service into the remaining handlers that
		// independently build an Actor with a hardcoded isAdmin=false, so
		// real platform admins are not silently denied profiles/billing,
		// credit reservations, ledger, and usage analytics access.
		profilesHandler = profilesHandler.WithRoleService(roleSvc)
		accountingHandler = accountingHandler.WithRoleService(roleSvc)
		ledgerHandler = ledgerHandler.WithRoleService(roleSvc)
		usageHandler = usageHandler.WithRoleService(roleSvc)

		grantsRepo := grants.NewPgxRepository(pool)
		grantsSvc := grants.NewService(grantsRepo, roleSvc)
		grantsHandler = grants.NewHandler(grantsSvc)
		log.Println("credit grants module ready (owner-discretionary)")

		// Phase 19 Plan 02 — identity + auth wiring (Task 9).
		// Builds the audit Logger (Sync+WAL) shared by the signup webhook
		// and tenant switch endpoint, the OWUI admin client, and the
		// signup tenant resolver. Required env vars are validated here
		// rather than at request time so misconfiguration is surfaced at
		// startup. Routes are mounted further down once the router mux
		// exists.
		owuiBaseURL := strings.TrimSpace(os.Getenv("OWUI_BASE_URL"))
		owuiAdminToken := strings.TrimSpace(os.Getenv("OWUI_ADMIN_TOKEN"))
		signupSecret := strings.TrimSpace(os.Getenv("SUPABASE_WEBHOOK_SECRET"))
		supabaseServiceRoleKey := strings.TrimSpace(os.Getenv("SUPABASE_SERVICE_ROLE_KEY"))

		auditSync := audit.NewSyncWriter(pool, audit.WriterConfig{
			DeploySHA: os.Getenv("DEPLOY_SHA"),
			Env:       os.Getenv("HIVE_ENV"),
		})
		auditWALDir := strings.TrimSpace(os.Getenv("AUDIT_WAL_DIR"))
		if auditWALDir == "" {
			auditWALDir = "/var/lib/hive/audit-wal"
		}
		walWriter, walErr := audit.NewWALWriter(audit.WALConfig{
			Dir:  auditWALDir,
			Sync: auditSync,
		})
		if walErr != nil {
			log.Fatalf("audit WAL init failed: %v", walErr)
		}
		auditWAL = walWriter
		auditLogger = audit.NewLogger(audit.LoggerDeps{Sync: auditSync, WAL: walWriter})
		log.Println("phase-19 audit logger ready")

		// Signup precheck (issue #116): disposable-domain + per-IP rate limit +
		// Turnstile CAPTCHA. Wired here (not gated on the phase-19 identity env
		// vars) so abuse controls run on every deployment. The per-IP limiter
		// reuses the control-plane Redis client; a nil client (Redis down at
		// startup) disables only the rate limit while disposable + CAPTCHA keep
		// working. The limiter fails CLOSED on a backend error per the #51
		// policy unless RATE_LIMIT_FAIL_OPEN=true.
		signupLimiter := signupguard.NewRateLimiter(
			signupguard.NewRedisIncrementer(redisClient),
			signupguard.RateLimitConfig{
				Limit:    cfg.SignupRateLimitPerWindow,
				Window:   cfg.SignupRateLimitWindow,
				FailOpen: cfg.SignupRateLimitFailOpen,
			},
		)
		turnstile := signupguard.NewTurnstileVerifier(cfg.TurnstileSecretKey, nil)
		if !turnstile.Enabled() {
			log.Println("WARNING: signupguard captcha disabled (TURNSTILE_SECRET_KEY unset)")
		}
		signupPrecheck = signupguard.NewHandler(signupguard.HandlerDeps{
			Blocklist:         disposableBlocklist,
			RateLimiter:       signupLimiter,
			Turnstile:         turnstile,
			AuditFunc:         signupGuardAudit(auditLogger),
			TrustedProxyCIDRs: cfg.TrustedProxyCIDRs,
			MaxConcurrent:     cfg.PrecheckMaxConcurrent,
			PrecheckTimeout:   time.Duration(cfg.PrecheckTimeoutSeconds) * time.Second,
		})
		log.Println("signupguard precheck ready (issue #116)")

		// Signup tenant provisioning (D-023). Deliberately NOT gated on the
		// optional identity variables read above. This is the path that turns a
		// new identity into a usable tenant member. Its original driver was a
		// Supabase Database Webhook configured in a dashboard, whose deletion
		// leaves no diff, no error and no failing test, and the env gate that
		// used to wrap this block took the repository-side replacement down
		// with it on any deployment that had not set OWUI_ADMIN_TOKEN and
		// SUPABASE_WEBHOOK_SECRET. The live demo box was in exactly that state,
		// a WARNING at boot, a healthy process, and no reachable provisioning
		// path at all. Provisioning needs the pool, the resolver and the audit
		// logger, and all three exist here unconditionally, so it is wired
		// unconditionally and reports its own readiness on the health endpoint.
		signupDeps := signup.WebhookDeps{
			Pool:     pool,
			Resolver: signup.NewPgxResolver(pool),
			Audit:    auditLogger,
			// Disposable-domain backstop (issue #116) for scripted signups
			// that hit Supabase directly and bypass the web-console precheck.
			DisposableCheck: disposableBlocklist.IsDisposableEmail,
			// Personal-tenant provisioning for a signup no tenant claims
			// (issue #625). Hive Cloud only. config.IsEnterprisePosture
			// is the single source of truth for this branch (issue
			// #653). It also gates the operator backfill command and the
			// licensing.FileSource vs licensing.CloudSource switch below.
			SelfServeTenants: !config.IsEnterprisePosture(cfg.LicenseFilePath),
			SharedSecret:     signupSecret,
		}
		// Open WebUI group wiring is a chat-side convenience, so a missing
		// admin token now costs exactly that and nothing else. Provisioner logs
		// the skip and still writes the tenant membership. Fataling here instead
		// would take billing, API-key resolution and the whole control-plane
		// down for a chat group.
		owuiConfigured := owuiBaseURL != "" && owuiAdminToken != ""
		if !owuiConfigured {
			log.Println("WARNING: Open WebUI group wiring disabled, OWUI_BASE_URL or OWUI_ADMIN_TOKEN unset. Tenant memberships are still provisioned.")
		}
		if owuiConfigured {
			owuiClient = owui.New(owui.Config{
				BaseURL:    owuiBaseURL,
				AdminToken: owuiAdminToken,
			})
			signupDeps.EnsureGroup = owuiClient.EnsureGroup
			signupDeps.AddUser = owuiClient.AddUserToGroup
		}
		if supabaseServiceRoleKey == "" {
			log.Println("WARNING: SUPABASE_SERVICE_ROLE_KEY unset. The tenant switch handler updates auth.users through the pool, which already carries service-role privilege.")
		}
		// Read at startup so a production deployment surfaces the omission
		// early, but not threaded into a handler today.
		_ = supabaseServiceRoleKey

		signupProvisioner := signup.NewProvisioner(signupDeps)

		// Per-user throttle on the console-driven provisioning route. Keyed
		// on the authenticated user id, in its own Redis namespace so it
		// cannot share a counter with the per-IP signup limiter. A nil
		// Redis client disables it, the same way it disables the signup
		// limiter, rather than blocking provisioning outright.
		provisionLimiter := signupguard.NewRateLimiter(
			signupguard.NewRedisIncrementer(redisClient),
			signupguard.RateLimitConfig{
				Limit:     cfg.TenantProvisionRateLimitPerWindow,
				Window:    cfg.TenantProvisionRateLimitWindow,
				FailOpen:  cfg.SignupRateLimitFailOpen,
				Namespace: "provision",
				Subject:   "user",
			},
		)
		// Second entry point into the same provisioning implementation, for the
		// console. A token carrying no tenant claim calls it on its first
		// authenticated request.
		signupViewerHandler = signup.NewViewerHandler(signupProvisioner, provisionLimiter.Allow)

		// Third entry point, and the only one that depends on nobody having
		// configured anything, the sweep. It asks the database which identities
		// hold no membership and runs the same Provisioner for each, so an
		// administrator creating a user through the Supabase admin API gets
		// that user provisioned without a dashboard webhook, without the
		// console being visited, and without a shared secret existing.
		signupReconciler = signup.NewReconciler(pool, signupProvisioner, signup.ReconcilerConfig{})
		signupReconciler.Start(runCtx)
		log.Println("signup provisioning ready (console route plus reconciler sweep)")

		// The legacy Supabase Database Webhook target. Kept wired so a
		// deployment that does still have that webhook configured behaves
		// exactly as before, and mounted only when the shared secret exists,
		// since the handler answers 500 to every request without one.
		if signupSecret == "" {
			log.Println("signup webhook route not mounted, SUPABASE_WEBHOOK_SECRET unset. Provisioning does not depend on it.")
		}
		if signupSecret != "" {
			signupWebhook = signup.NewWebhook(signupDeps)
		}

		tenantsHandler = tenants.NewHandler(tenants.Deps{Pool: pool, Audit: auditLogger})

		// Tenant model visibility admin routes. The handler type shipped with
		// Phase 20 Plan 04 but was never constructed here, so
		// /internal/catalog/visibility/* answered 404 and the admin control had
		// no reachable write path at all. Route selection now enforces the same
		// visibility rules, which makes this the surface that turns a model off
		// for a tenant, so it has to be mounted.
		if catalogSvc != nil {
			// Pass a nil interface (not a typed-nil client) when OWUI is not
			// configured, so syncOWUI's nil check actually fires.
			var owuiSync catalog.OWUISync
			if owuiClient != nil {
				owuiSync = owuiClient
			}
			catalogVisibilityHandler = catalog.NewVisibilityHandler(catalogSvc, owuiSync)
			log.Println("tenant model visibility admin routes registered (Phase 20 Plan 04)")

			// Issue #772 — the OWUI chat picker listed hive-embedding-default,
			// hive-stt and hive-tts as selectable chat models: picking one
			// produced a broken conversation. syncOWUI now locks non-chat-
			// modality aliases out of the OWUI picker, but that function only
			// runs from the admin PUT/DELETE visibility mutation path, which a
			// migration-seeded alias (all three of the above) never goes
			// through. Reconcile once at boot so the fix actually applies to
			// rows already sitting in model_aliases, and so it re-applies on
			// its own after an Open WebUI image bump resets access_control.
			// Best-effort: a failure here logs and does not block startup, the
			// same posture syncOWUI itself takes for a single alias.
			if owuiClient != nil {
				if err := catalogVisibilityHandler.ReconcileOWUISync(runCtx); err != nil {
					log.Printf("WARNING: owui non-chat-modality reconcile failed (issue #772): %v", err)
				} else {
					log.Println("owui non-chat-modality reconcile complete (issue #772)")
				}
			}
		}

		configuredSinks := configuredAuditSinks()
		if len(configuredSinks) == 0 {
			log.Println("phase-19 audit sink worker idle (no optional sinks configured)")
		} else {
			worker := auditworker.New(auditworker.Config{Pool: pool, Sinks: configuredSinks})
			go worker.Run(runCtx)
			log.Printf("phase-19 audit sink worker started (sinks=%d)", len(configuredSinks))
		}

		if auditWAL != nil {
			go waldrainer.Run(runCtx, auditWAL, 30*time.Second)
			log.Println("phase-19 audit WAL drainer started")
		}

		verifier := auditverifier.New(pool)
		runVerify := func() {
			mismatches, err := verifier.VerifyPartition(runCtx, time.Now())
			if err != nil {
				log.Printf("audit chain verifier failed: %v", err)
				if auditLogger != nil {
					if logErr := auditLogger.Log(runCtx, audit.Event{
						Action:   "AUDIT_VERIFY_ERROR",
						Severity: audit.SeverityError,
						Actor:    audit.Actor{Type: audit.ActorSystem},
						Before:   map[string]string{"error": err.Error()},
					}); logErr != nil {
						log.Printf("audit_verify_error log emit failed: %v", logErr)
					}
				}
				return
			}
			if mismatches > 0 && auditLogger != nil {
				if logErr := auditLogger.Log(runCtx, audit.Event{
					Action:   "AUDIT_CHAIN_VERIFY_FAIL",
					Severity: audit.SeverityCritical,
					Actor:    audit.Actor{Type: audit.ActorSystem},
					Before:   map[string]int{"mismatches": mismatches},
				}); logErr != nil {
					log.Printf("audit_chain_verify_fail log emit failed: %v", logErr)
				}
			}
		}
		go func() {
			// Run one verification pass at startup. Pods restart more
			// frequently than the 24h ticker fires, so without this
			// chain corruption could go undetected for an arbitrary
			// number of deploys before the daily check.
			runVerify()
			ticker := time.NewTicker(24 * time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-runCtx.Done():
					return
				case <-ticker.C:
					runVerify()
				}
			}
		}()
		log.Println("phase-19 audit chain verifier scheduled (initial pass at startup, then daily)")

		// Audit cold-archive cron (PHIPA 10-year / Quebec Law 25).
		// Reads hot-retention window and retention years from env; defaults to
		// 90-day hot window and 10-year cold retention if unset.
		archiveCronInterval := parseDurationEnv("AUDIT_COLD_ARCHIVE_CRON_INTERVAL", 24*time.Hour)
		archiveRepo := auditarchive.NewPgRepository(pool)
		archiveStore := auditarchive.NewStorageObjectStore(storageClient, os.Getenv("AUDIT_COLD_ARCHIVE_BUCKET"), strings.TrimSpace(os.Getenv("S3_ENDPOINT")))
		archiver := auditarchive.New(auditarchive.Config{
			HotRetentionDays:  parseIntEnv("AUDIT_COLD_ARCHIVE_HOT_DAYS", 90),
			RetentionYears:    parseIntEnv("AUDIT_COLD_ARCHIVE_RETENTION_YEARS", 10),
			ColdStorageBucket: envOr("AUDIT_COLD_ARCHIVE_BUCKET", "hive-audit-cold"),
			Repo:              archiveRepo,
			Store:             archiveStore,
		})
		go func() {
			if err := archiver.RunCron(runCtx, archiveCronInterval); err != nil && err != context.Canceled {
				log.Printf("audit cold-archive cron exited: %v", err)
			}
		}()
		log.Printf("audit cold-archive cron started (hot_days=%d, retention_years=%d, interval=%s)",
			parseIntEnv("AUDIT_COLD_ARCHIVE_HOT_DAYS", 90),
			parseIntEnv("AUDIT_COLD_ARCHIVE_RETENTION_YEARS", 10),
			archiveCronInterval,
		)
	} else {
		log.Println("WARNING: accounts routes not available — database pool not ready")
	}

	// Payments service wiring (requires DB pool; handler is nil when pool unavailable).
	var paymentsHandler *payments.Handler
	if pool != nil {
		paymentHTTPClient := &http.Client{Timeout: 30 * time.Second}

		// FX service — wraps XE API with Redis cache.
		fxSvc := payments.NewFXService(paymentHTTPClient, xeAccountID, xeAPIKey, redisClient)

		// Rails — conditionally registered based on env var presence.
		rails := make(map[payments.Rail]payments.PaymentRail)
		if stripeSecretKey != "" {
			rails[payments.RailStripe] = stripeRail.NewRail(stripeSecretKey, stripeWebhookSecret)
		}
		if bkashAppKey != "" {
			rails[payments.RailBkash] = bkashRail.NewRail(paymentHTTPClient, bkashBaseURL, bkashAppKey, bkashAppSecret, bkashUsername, bkashPassword)
		}
		if sslcommerzStoreID != "" {
			rails[payments.RailSSLCommerz] = sslcommerzRail.NewRail(paymentHTTPClient, sslcommerzBaseURL, sslcommerzStoreID, sslcommerzStorePasswd)
		}

		log.Printf("payments: %d rail(s) active: %v", len(rails), func() []string {
			names := make([]string, 0, len(rails))
			for r := range rails {
				names = append(names, string(r))
			}
			return names
		}())

		paymentsRepo := payments.NewPgxRepository(pool)

		// Hard-fail unless the payment stub is allowed to run in this
		// environment. The allowlist (demo, staging, local, development, test)
		// lives in paymentStub.CheckProductionSafety as the single source of
		// truth; an unset or unrecognised HIVE_ENV fails closed so the
		// instant-credit stub can never silently activate in real production.
		paymentStub.CheckProductionSafety()

		var paymentsSvc payments.PaymentService
		if paymentStub.IsEnabled() {
			// Demo stub mode: credits are granted immediately through the real
			// ledger; no payment rail is called. Gate: HIVE_PAYMENTS_STUB=true.
			// ledgerGrantAdapter wraps ledger.Service to satisfy the stub's
			// LedgerGranter interface (returns error only, discards LedgerEntry).
			// stubCountryAdapter lets the stub apply the same country to rail
			// access control as production via payments.AvailableRails.
			paymentsSvc = paymentStub.NewStubService(
				&ledgerGrantAdapter{svc: ledgerSvc},
				&stubCountryAdapter{svc: profilesSvc},
			)
		} else {
			realSvc := payments.NewService(paymentsRepo, ledgerSvc, profilesSvc, fxSvc, rails)
			paymentsSvc = realSvc

			// M1: BD-payments confirmation loop — only runs when the stub is OFF (i.e. real payment rails active).
			// Bound to runCtx so it runs for the lifetime of the server, not
			// the 10s startup ctx. Each tick uses runCtx so graceful shutdown
			// aborts an in-flight rail call instead of letting it linger.
			go func() {
				ticker := time.NewTicker(60 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						confirmed, err := realSvc.ConfirmPendingBDPayments(runCtx)
						if err != nil {
							log.Printf("payments: error confirming BD payments: %v", err)
						} else if confirmed > 0 {
							log.Printf("payments: confirmed %d pending BD payment(s)", confirmed)
						}
					case <-runCtx.Done():
						return
					}
				}
			}()
		}
		paymentsHandler = payments.NewHandler(paymentsSvc, &accountsResolverAdapter{svc: accountsSvc})
	}

	// Build Prometheus metrics registry before the router so the instrumentation
	// middleware and the telemetry listener share one registry.
	metricsRegistry, promRegistry := metrics.NewRegistry()
	// A failing sweep is reported on the telemetry listener, not on the
	// readiness endpoint. Provisioning can be broken while API-key
	// resolution, routing, accounting and payment webhooks still work, so
	// degrading the container healthcheck for it would turn a signup outage
	// into a billing one, and a restart would reset the counter and repeat
	// (review finding, CodeRabbit on PR 993). The absence of the wiring
	// itself still degrades readiness, because that one cannot be transient.
	if signupReconciler != nil {
		go func() {
			ticker := time.NewTicker(provisioningGaugeInterval)
			defer ticker.Stop()
			for {
				failures := signupReconciler.ConsecutiveFailures()
				metricsRegistry.SignupProvisioningSweepFailures.Set(float64(failures))
				select {
				case <-runCtx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
		// The gauge above resets on any clean sweep, and a sweep goes clean the
		// moment the identity that kept faulting ages out of the reconciler's
		// lookback window, so an alert on it alone resolves itself exactly when
		// provisioning has permanently failed for somebody (review finding on
		// PR 993). These two do not: a monotonic fault counter, and a count of
		// identities already past the window holding no membership. Both read
		// cached reconciler state, so a scrape never waits on the database.
		if err := metrics.RegisterSignupProvisioning(promRegistry, signupReconciler); err != nil {
			// Loud rather than ignored: without these collectors a permanent
			// provisioning failure is invisible again, which is the defect.
			log.Printf("metrics: signup provisioning collectors not registered: %v", err)
		}
	}

	// Create the mux upfront so filestore.RegisterRoutes (which requires *http.ServeMux)
	// can register routes on it before the instrumentation wrapper is applied.
	routerMux := http.NewServeMux()

	if cfg.InternalToken == "" {
		slog.Warn("CONTROL_PLANE_INTERNAL_TOKEN is not set; /internal/* service-to-service endpoints are UNAUTHENTICATED. Set it (and the matching value on edge-api) in any non-local deployment.")
	}

	// Identity: finalize email verification for the authenticated caller (#112).
	// The privileged write lives here (the pool carries service-role DB
	// privilege) instead of in the web-console edge route, and only flips the
	// flag when Supabase has already confirmed the email. When pool is nil the
	// handler is left unwired and the route is not registered (the request 404s
	// at the mux) — the control-plane does not run a real identity flow without
	// a database anyway. The handler's own nil-dependency guard returns a loud
	// 500 for the wired-but-misconfigured case (a future caller that constructs
	// it without FinalizeEmailVerified).
	var identityHandler *identity.Handler
	if pool != nil {
		p := pool
		identityHandler = identity.NewHandler(identity.Deps{
			Audit: auditLogger,
			FinalizeEmailVerified: func(ctx context.Context, userID uuid.UUID) (int64, error) {
				tag, err := p.Exec(ctx,
					`UPDATE auth.users
					    SET raw_app_meta_data = COALESCE(raw_app_meta_data, '{}'::jsonb)
					      || jsonb_build_object('hive_email_verified', true)
					  WHERE id = $1
					    AND email_confirmed_at IS NOT NULL`, userID)
				if err != nil {
					return 0, err
				}
				return tag.RowsAffected(), nil
			},
		})
	}

	// Issue #238 — feature gate handler. Resolves per-tenant flags from the
	// tenant_settings table via a 30 s in-process cache (settings.Resolver).
	// Edge-api calls GET /internal/featuregate/{tenant_id} to populate its own
	// 30 s edge cache, giving end-to-end revocation in under 60 s.
	var featureGateHandler *featuregate.Handler
	var featureGateAdminHandler *featuregate.AdminHandler
	if pool != nil {
		settingsResolver := settings.NewResolver(pool, 30*time.Second)
		featureGateHandler = featuregate.NewHandler(settingsResolver)
		// Issue #292 (blueprint Step 1.2) — admin feature-gate CRUD reuses the
		// same resolver so a PUT invalidates the cache the internal GET reads.
		featureGateAdminHandler = featuregate.NewAdminHandler(settingsResolver)
	}

	// Issue #308 — egress policy single source of truth. Admin CRUD is
	// owner-gated via tenantRoleSvc.IsTenantOwner: egress_policies is keyed by
	// tenant_id, so authority comes from public.tenant_users, not from the
	// account-scoped roleSvc. Neither the server-side OpenHands allowed_hosts
	// consumer nor the desktop firewall rule generator is wired here.
	var egressPolicyHandler *egress.Handler
	var egressSvc *egress.Service
	var tenantRoleSvc *platform.TenantRoleService
	if pool != nil {
		egressRepo := egress.NewPgxRepository(pool)
		tenantRoleSvc = platform.NewTenantRoleService(platform.NewPgxTenantRoleStore(pool))
		egressSvc = egress.NewService(egressRepo, tenantRoleSvc)
		egressPolicyHandler = egress.NewHandler(egressSvc)
	}

	// Issue #758 — the workspace-scoped admin surfaces (feature gates, the
	// marketplace) are gated on the OWNER of the tenant in scope, with the
	// platform-admin overlay still admitted and still required for the platform
	// operations carved out inside those handlers. Built only when both halves
	// resolve, so the routes are skipped rather than mounted behind half a gate.
	// Declared as the interface the router consumes, not as the concrete pointer:
	// a nil *WorkspaceAdminGate stored in an interface field is non-nil to the
	// router, which would mount the routes behind a gate that panics.
	var workspaceAdminGate interface {
		Require(http.Handler) http.Handler
	}
	if tenantRoleSvc != nil && roleSvc != nil {
		workspaceAdminGate = platform.NewWorkspaceAdminGate(tenantRoleSvc, roleSvc)
	}

	// Issue #309 (blueprint Step 2.3) — MCP and skills marketplace, admin-
	// curated baseline. Per-tenant enablement is workspace-owner gated and
	// catalog curation stays platform-admin only (issue #758). The internal
	// read surface is the seam apps/agent-engine/internal/marketplaceclient
	// consumes to build a session's MCP config.
	var marketplaceHandler *marketplace.Handler
	if pool != nil {
		marketplaceRepo := marketplace.NewPgxRepository(pool)
		marketplaceSvc := marketplace.NewService(marketplaceRepo)
		marketplaceHandler = marketplace.NewHandler(marketplaceSvc)
	}

	// Issue #311 (blueprint Step 3.4) — agent task persistence, web side.
	// Issue #305 closes the control-channel half of the Wave 3 gap; see
	// buildAgentEngine's doc comment and
	// apps/control-plane/internal/agenttask/SYNC_CONTRACT.md's Engine seam
	// section for why the real Engine is still conditional on deployment
	// env vars rather than unconditionally wired.
	var (
		agentTaskHandler     *agenttask.Handler
		agentScheduleHandler *agentsched.Handler
		userMemoriesHandler  *usermemories.Handler
	)
	if pool != nil {
		agentTaskRepo := agenttask.NewPgxRepository(pool)
		agentEngine, agentEngineStatus, agentEngineEvents := buildAgentEngine(egressSvc)
		agentTaskSvc := agenttask.NewService(agentTaskRepo, agentEngine,
			agenttask.WithEventSource(agentEngineEvents))
		agentTaskHandler = agenttask.NewHandler(agentTaskSvc)

		// Issue #172 (ruling D-020): cross-chat user memory, four-verb
		// internal surface. Recall reads the same rows directly in edge-api's
		// chat dispatch path; nothing else consumes them yet.
		memoryRepo := usermemories.NewPgxRepository(pool)
		memorySvc := usermemories.NewService(memoryRepo)
		userMemoriesHandler = usermemories.NewHandler(memorySvc)

		// Poller needs a real StatusChecker to poll — NotConfiguredEngine has
		// no Status method — so it is only started when the engine itself
		// is (same HIVE_AGENT_ENGINE_* gate; see SYNC_CONTRACT.md's Engine
		// seam section). The event syncer shares that gate: it needs the same
		// live engine surface, and its per-task failures degrade to missed
		// events, never wrong ones.
		if agentEngineStatus != nil {
			interval := parseDurationEnv("HIVE_AGENT_TASK_POLL_INTERVAL", 15*time.Second)
			poller := agenttask.NewPoller(agentTaskRepo, agentEngineStatus, agenttask.PollerConfig{
				Interval: interval,
				Logger:   slog.Default(),
			})
			// Bound to runCtx so it stops cleanly on shutdown, same as the
			// other background workers below (spend-alert cron, WAL drainer).
			poller.Start(runCtx)
			defer poller.Stop()
			log.Println("agent task status poller started")

			if agentEngineEvents != nil {
				syncer := agenttask.NewEventSyncer(agentTaskRepo, agentEngineEvents, agenttask.PollerConfig{
					Interval: interval,
					Logger:   slog.Default(),
				})
				syncer.Start(runCtx)
				defer syncer.Stop()
				log.Println("agent task event syncer started")
			}
		}

		// Scheduled agent tasks ("routines"): the CRUD surface plus the
		// minute tick that turns due schedules into real tasks through
		// agentTaskSvc.CreateTask — the SAME service path a manual creation
		// uses, so metering, quota and engine gating apply to scheduled runs
		// identically. The scheduler only starts when the engine itself is
		// configured (same gate as the poller above): without an engine every
		// scheduled run would fail into last_error and burn a cadence per
		// deployment restart for nothing.
		scheduleRepo := agentsched.NewPgxRepository(pool)
		scheduleSvc := agentsched.NewService(scheduleRepo, nil)
		agentScheduleHandler = agentsched.NewHandler(scheduleSvc)

		if agentEngineStatus != nil {
			scheduler := agentsched.NewScheduler(scheduleRepo, agentTaskSvc, agentsched.SchedulerConfig{
				Interval: parseDurationEnv("HIVE_AGENT_SCHEDULER_INTERVAL", time.Minute),
				Logger:   slog.Default(),
			})
			scheduler.Start(runCtx)
			defer scheduler.Stop()
			log.Println("agent task scheduler started")
		}
	}

	router := platformhttp.NewRouter(platformhttp.RouterConfig{
		// pool != nil is the same condition that gates every DB-backed
		// handler above, so /health cannot report ok while those routes are
		// absent (issue #816). resolveHealth.Degraded() adds the runtime
		// half: a pool that opened fine at boot and later started failing
		// checkouts under contention (issue #836).
		DBReady: dbReadyFunc(pool, resolveHealth),
		// Signup provisioning readiness (D-023). A nil reconciler answers false
		// through this same method value, so a deployment that failed to wire
		// provisioning reports degraded rather than serving traffic quietly.
		ProvisioningReady:        signupReconciler.Ready,
		AuthMiddleware:           authMiddleware,
		AccountsHandler:          accountsHandler,
		IdentityHandler:          identityHandler,
		AccountingHandler:        accountingHandler,
		APIKeysHandler:           apikeysHandler,
		BudgetsHandler:           budgetsHandler,
		CatalogHandler:           catalogHandler,
		CatalogVisibilityHandler: catalogVisibilityHandler,
		LedgerHandler:            ledgerHandler,
		PaymentsHandler:          paymentsHandler,
		ProfilesHandler:          profilesHandler,
		ProvidersRouter:          providersHandler,
		LiteLLMSyncHandler:       litellmSyncHandler,
		FeatureGateHandler:       featureGateHandler,
		FeatureGateAdminHandler:  featureGateAdminHandler,
		EgressPolicyHandler:      egressPolicyHandler,
		MarketplaceHandler:       marketplaceHandler,
		AgentTaskHandler:         agentTaskHandler,
		AgentScheduleHandler:     agentScheduleHandler,
		UserMemoriesHandler:      userMemoriesHandler,
		RoutingHandler:           routingHandler,
		UsageHandler:             usageHandler,
		MetricsRegistry:          metricsRegistry,
		Mux:                      routerMux,
		InternalToken:            cfg.InternalToken,
		RoleSvc:                  roleSvc,
		WorkspaceAdminGate:       workspaceAdminGate,
	})

	// Wire filestore internal endpoints if the database pool is available.
	if pool != nil {
		filestoreRepo, err := filestore.NewRepository(pool)
		if err != nil {
			log.Printf("WARNING: filestore schema setup failed: %v", err)
		} else {
			filestoreSvc := filestore.NewService(filestoreRepo)
			var batchSubmitter filestore.BatchSubmitter

			// Start Asynq batch polling worker if Redis is available.
			if cfg.RedisURL != "" {
				redisOpt, parseErr := asynq.ParseRedisURI(cfg.RedisURL)
				if parseErr != nil {
					log.Printf("WARNING: could not parse Redis URL for asynq worker: %v", parseErr)
				} else {
					asynqClient := asynq.NewClient(redisOpt)
					defer asynqClient.Close()

					asynqQueue := batchstore.NewAsynqQueue(asynqClient)
					if routingSvc != nil && accountingSvc != nil {
						batchSubmitter = batchstore.NewSubmitter(
							filestoreSvc,
							routingSvc,
							storageClient,
							asynqQueue,
							accountingSvc,
							resolveLiteLLMBaseURL(),
							resolveLiteLLMMasterKey(),
							storageCfg.FilesBucket,
						).WithLocalExecutor(asynqQueue, cfg.BatchExecutorKind)
					}

					batchWorker := batchstore.NewBatchWorker(
						filestoreSvc,
						resolveLiteLLMBaseURL(),
						resolveLiteLLMMasterKey(),
						storageClient,
						storageCfg.FilesBucket,
						accountingSvc,
					)

					// Phase 15: build local executor and wire into worker.
					if routingSvc != nil && accountingSvc != nil {
						execCfg := batchexecutor.Config{
							Concurrency: cfg.BatchExecutorConcurrency,
							MaxRetries:  cfg.BatchExecutorMaxRetries,
							LineTimeout: time.Duration(cfg.BatchExecutorLineTimeoutMs) * time.Millisecond,
							Kind:        batchexecutor.ExecutorKind(cfg.BatchExecutorKind),
						}
						inferenceClient := batchstore.NewLiteLLMInferenceClient(resolveLiteLLMBaseURL(), resolveLiteLLMMasterKey())
						dispatcher, dispErr := batchexecutor.NewDispatcher(execCfg, inferenceClient, nil)
						if dispErr != nil {
							log.Printf("WARNING: batch executor dispatcher init failed: %v", dispErr)
						} else {
							batchStore := batchstore.NewPgxBatchStore(filestoreSvc, filestoreSvc, routingSvc)
							lineStore := batchstore.NewPgxLineStore(pool)
							reservationPort := batchstore.NewAccountingReservationAdapter(accountingSvc)
							fileRegistrar := batchstore.NewPgxFileRegistrar(filestoreSvc)
							ex, exErr := batchexecutor.NewExecutor(execCfg, batchStore, lineStore, storageClient, fileRegistrar, storageCfg.FilesBucket, dispatcher, reservationPort)
							if exErr != nil {
								log.Printf("WARNING: batch executor init failed: %v", exErr)
							} else {
								batchWorker.WithLocalExecutor(ex)
								log.Printf("batch local executor ready (concurrency=%d kind=%s)", execCfg.Concurrency, execCfg.Kind)
							}
						}
					}

					asynqMux := asynq.NewServeMux()
					asynqMux.HandleFunc(batchstore.TypeBatchPoll, batchWorker.HandleBatchPoll)
					asynqMux.HandleFunc(batchstore.TypeBatchExecute, batchWorker.HandleBatchExecute)

					asynqSrv := asynq.NewServer(
						redisOpt,
						asynq.Config{
							Concurrency: 5,
							Queues:      map[string]int{"batch": 1, "default": 1},
							RetryDelayFunc: func(_ int, _ error, _ *asynq.Task) time.Duration {
								return 30 * time.Second
							},
						},
					)
					go func() {
						if err := asynqSrv.Run(asynqMux); err != nil {
							log.Printf("batch worker stopped: %v", err)
						}
					}()
					log.Println("batch worker started")
				}
			}

			filestore.RegisterRoutes(routerMux, filestoreSvc, batchSubmitter, func(h http.Handler) http.Handler {
				return platformhttp.RequireInternalToken(cfg.InternalToken, h)
			})
			log.Println("filestore routes registered")
		}

		// RAG ingestion (#232): edge-api registers the rag_documents row, then
		// calls this internal endpoint to chunk, embed, and store the content.
		// Requires an embedding backend; without one the route is not mounted
		// and uploaded documents simply stay "pending" (no partial pipeline).
		ragEmbedBaseURL := strings.TrimSpace(os.Getenv("EMBEDDING_BASE_URL"))
		ragEmbedModel := strings.TrimSpace(os.Getenv("EMBEDDING_MODEL"))
		if ragEmbedModel == "" {
			ragEmbedModel = "bge-m3"
		}
		if ragEmbedBaseURL == "" {
			log.Println("WARNING: EMBEDDING_BASE_URL not set; rag ingest route not registered (uploaded documents will stay pending)")
		} else {
			// EMBEDDING_DIM: overwrites cprag.EmbeddingDimension's default
			// (1024) so ingest's dimension checks and rag_documents.embedding_dim
			// provenance both follow config, not a compile-time constant.
			ragEmbedDimRaw := strings.TrimSpace(os.Getenv("EMBEDDING_DIM"))
			ragEmbedDim := cprag.EmbeddingDimension
			if ragEmbedDimRaw != "" {
				if v, err := strconv.Atoi(ragEmbedDimRaw); err != nil {
					log.Printf("WARNING: EMBEDDING_DIM=%q is not a valid integer; falling back to default %d", ragEmbedDimRaw, ragEmbedDim)
				} else {
					ragEmbedDim = v
				}
			}
			cprag.EmbeddingDimension = ragEmbedDim

			// EMBEDDING_ALLOW_UNINDEXED: see edge-api/cmd/server/main.go. Opts a
			// >4000-dim configuration into an unindexed brute-force column.
			ragAllowUnindexedRaw := strings.TrimSpace(os.Getenv("EMBEDDING_ALLOW_UNINDEXED"))
			ragEmbedAllowUnindexed := ragAllowUnindexedRaw == "1" || strings.EqualFold(ragAllowUnindexedRaw, "true")

			// Cross-service reconcile (#368): compare env config against the
			// active rag_embedding_config row the column was provisioned to.
			ragConfigMatches := true
			if active, found, cerr := cprag.LoadActiveConfig(runCtx, pool); cerr != nil {
				log.Printf("WARNING: could not read rag_embedding_config, proceeding on env config only: %v", cerr)
			} else if found && !embedmodel.SameConfig(ragEmbedModel, ragEmbedDim, active.Model, active.Dim) {
				ragConfigMatches = false
			}

			// embedmodel.Resolve is the single source of truth: it derives the
			// MRL reduction target (plan.ReduceTo) and rejects non-selectable
			// combinations. There is no independent EMBEDDING_TRUNCATE_TO knob.
			// A rejected or config-mismatched combination leaves the ingest
			// route unmounted, the same fail-closed posture as an unset
			// EMBEDDING_BASE_URL above.
			plan, rerr := embedmodel.Resolve(ragEmbedModel, ragEmbedDim, ragEmbedAllowUnindexed)
			switch {
			case rerr != nil:
				log.Printf("ERROR: RAG embedding configuration is inconsistent, rag ingest route not registered: %v", rerr)
			case !ragConfigMatches:
				log.Printf("ERROR: RAG embedding config mismatch (env model=%s dim=%d) vs provisioned rag_embedding_config, rag ingest route not registered; provision + re-embed to switch models", ragEmbedModel, ragEmbedDim)
			default:
				// plan.PgType selects the SearchChunks query-vector cast so a
				// halfvec-provisioned column is queried with ::halfvec.
				ragRepo := cprag.NewRepo(pool, plan.PgType)
				ragEmbedClient := cprag.NewHTTPEmbedClient(ragEmbedBaseURL, ragEmbedModel, plan.ReduceTo, resolveLiteLLMMasterKey())
				ragIngester := cprag.NewIngester(ragRepo, ragEmbedClient, 0, ragEmbedModel)
				cprag.RegisterRoutes(routerMux, func(ctx context.Context, tenantID, docID uuid.UUID, content string) error {
					return ragIngester.Ingest(ctx, tenantID, docID, content)
				}, func(h http.Handler) http.Handler {
					return platformhttp.RequireInternalToken(cfg.InternalToken, h)
				})
				log.Println("rag ingest route registered")

				// Catch-up re-embed: provisioning a new model/dim marks the
				// corpus pending; migrate any backlog onto the active config
				// once at startup so the per-tenant fail-closed guard reopens.
				// ponytail: single startup pass on the privileged pool (walks
				// every tenant), not a scheduler; a full re-embed queue + admin
				// trigger endpoint is the follow-up admin-surface PR.
				go func() {
					rb := cprag.NewReembedder(pool, ragEmbedClient, 0, ragEmbedModel, ragEmbedDim)
					done, remaining, rbErr := rb.RunOnce(runCtx)
					if rbErr != nil {
						log.Printf("rag re-embed catch-up error: %v", rbErr)
						return
					}
					if done > 0 || remaining > 0 {
						log.Printf("rag re-embed catch-up: %d migrated, %d still pending", done, remaining)
					}
				}()
			}
		}
	}

	// Phase 14 — register the invoices handler. Auth middleware gates
	// every customer route; the handler internally enforces workspace
	// membership via AccessChecker.
	if invoicesHandler != nil {
		protectedInvoices := authMiddleware.Require(invoicesHandler)
		routerMux.Handle("/api/v1/invoices", protectedInvoices)
		routerMux.Handle("/api/v1/invoices/", protectedInvoices)
		log.Println("invoices routes registered (Phase 14)")
	}

	// Issue #304 (D9) -- licensing entitlement seam. LICENSE_FILE_PATH set
	// selects Hive Enterprise mode (offline signed file, re-verified on a
	// schedule, no phone-home); empty selects Hive Cloud mode (sync-path
	// placeholder). Both satisfy licensing.Source identically, so the
	// handler never branches on deployment mode.
	var licenseSource licensing.Source
	if config.IsEnterprisePosture(cfg.LicenseFilePath) {
		licenseSource = licensing.FileSource{
			Path:         cfg.LicenseFilePath,
			PublicKeyB64: cfg.LicensePublicKeyB64,
		}
	} else {
		licenseSource = licensing.CloudSource{Entitlement: licensing.DefaultCloudEntitlement(time.Now())}
	}
	scheduledLicenseSource := &licensing.ScheduledSource{
		Inner:    licenseSource,
		Interval: time.Duration(cfg.LicenseRevalidateIntervalSeconds) * time.Second,
	}
	var licenseRecorder licensing.Recorder
	if pool != nil {
		licenseRecorder = licensing.PgxRecorder{Pool: pool}
	}
	licenseHandler := licensing.NewHandler(scheduledLicenseSource, licenseRecorder)
	routerMux.Handle("/internal/license/entitlement", platformhttp.RequireInternalToken(cfg.InternalToken, licenseHandler))
	log.Println("licensing entitlement route registered (issue #304)")

	// Issue #1063: the chat composer's credits banner. Open WebUI's backend
	// resolves its own signed-in user to an email server side and calls this
	// internal route; the tenant->billing-account link stays behind the
	// shared-secret gate and never reaches a browser. The route is not mounted
	// at all without a configured token: RequireInternalToken already fails
	// closed on an empty token, and skipping the mount makes a misconfigured
	// deployment read as silent absence rather than a live surface.
	if ledgerSvc != nil && cfg.InternalToken != "" {
		ledger.RegisterChatBalanceRoute(routerMux, ledgerSvc, func(h http.Handler) http.Handler {
			return platformhttp.RequireInternalToken(cfg.InternalToken, h)
		})
		log.Println("chat credits balance route registered (issue #1063)")
	}

	// Phase 14 — register credit grant routes. Admin surface gated via
	// RequirePlatformAdmin (provider-blind 401/403 sanitised JSON); self
	// surface gated via plain auth middleware.
	if grantsHandler != nil && roleSvc != nil && authzMW.Initialized() {
		registerCreditGrantRoutes(
			routerMux,
			authMiddleware.Require,
			authzMW,
			grantsHandler.AdminMux(),
			grantsHandler.SelfMux(),
		)
		log.Println("credit grants routes registered (Phase 14)")
	}

	// Phase 19 Plan 02 Task 9 — signup webhook + tenant switch routes.
	//
	// /internal/auth/user-created is a Supabase Database Webhook target;
	// the handler verifies the X-Hive-Signup-Secret header internally and
	// is intentionally unauthenticated at the middleware layer (Supabase
	// fires it without a bearer token). /v1/tenants/switch sits behind
	// the standard auth middleware.
	if signupWebhook != nil {
		routerMux.Handle("/internal/auth/user-created", signupWebhook)
		log.Println("signup webhook route registered (Phase 19)")
	}

	// Authenticated tenant-membership reconcile. The console calls this on the
	// first request from a user whose token carries no tenant claim, which is
	// the one thing such a token is meant to be able to do. The exact path
	// beats the authenticated /api/v1/ catch-all by ServeMux longest-prefix
	// match, same as the signup precheck below.
	if signupViewerHandler != nil && authMiddleware != nil {
		routerMux.Handle("/api/v1/viewer/tenant-provision",
			authMiddleware.Require(signupViewerHandler))
		log.Println("viewer tenant-provision route registered")
	}

	// Signup abuse-prevention precheck (issue #116). Public (no auth bearer —
	// the caller is not yet a Hive account); the web-console signup page calls
	// this before invoking Supabase signUp. The exact path beats the
	// authenticated /api/v1/ catch-all by ServeMux longest-prefix match.
	if signupPrecheck != nil {
		routerMux.Handle("/api/v1/auth/sign-up/precheck", signupPrecheck)
		log.Println("signup precheck route registered (issue #116)")
	}
	if tenantsHandler != nil {
		protectedSwitch := authMiddleware.Require(http.HandlerFunc(tenantsHandler.Switch))
		routerMux.Handle("/v1/tenants/switch", protectedSwitch)
		log.Println("tenants switch route registered (Phase 19)")
	}
	// owuiClient is reachable via the signup webhook today; keep the
	// reference live so future tasks (invite acceptance, tenant create)
	// can wire it without rebuilding the import graph.
	_ = owuiClient
	_ = auditLogger

	// Telemetry listener. The Prometheus series carry provider names
	// (hive_upstream_requests_total{provider=...}), payment-rail event counts,
	// ledger posting counts, and the full /internal/* endpoint inventory, so
	// /metrics is kept off the public listener: cfg.Port is published to the
	// host and routed through the ingress tunnel, this port is neither.
	// Prometheus scrapes it over the container network.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.HandlerFor(promRegistry, promhttp.HandlerOpts{}))
	metricsSrv := &http.Server{
		Addr:              metricsListenAddr,
		Handler:           metricsMux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("control-plane metrics listening on %s", metricsListenAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("metrics ListenAndServe: %v", err)
		}
	}()

	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("control-plane listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe: %v", err)
		}
	}()

	<-quit
	log.Println("shutting down control-plane...")

	// Signal Plan 19 audit workers, WAL drainer, and verifier loop to unwind
	// before HTTP shutdown closes the DB pool out from under them.
	runCancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("metrics server shutdown error: %v", err)
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("server shutdown error: %v", err)
	}
	log.Println("control-plane stopped")
}

func resolveLiteLLMBaseURL() string {
	if u := os.Getenv("LITELLM_BASE_URL"); u != "" {
		return u
	}
	return "http://litellm:4000"
}

func resolveLiteLLMMasterKey() string {
	if k := os.Getenv("LITELLM_MASTER_KEY"); k != "" {
		return k
	}
	return "litellm-dev-key"
}

type storageRuntimeConfig struct {
	Client      storage.Config
	FilesBucket string
}

// signupGuardAudit adapts the audit Logger to the signupguard.AuditFunc seam.
// Detail maps carry classification strings only (never the raw email/domain or
// any provider value), satisfying the BD provider-blind + audit-leak rules.
// A nil logger yields a no-op so the precheck still works when audit is absent.
func signupGuardAudit(logger *audit.Logger) signupguard.AuditFunc {
	if logger == nil {
		return nil
	}
	return func(ctx context.Context, action string, detail map[string]string) {
		_ = logger.Log(ctx, audit.Event{
			Action:   action,
			Severity: audit.SeverityWarning,
			Actor:    audit.Actor{Type: audit.ActorSystem},
			Before:   detail,
		})
	}
}

// tenantMembershipCheck reports whether a user holds a membership the token
// hook would also accept. The predicate is deliberately identical to the one in
// public.custom_access_token_hook and in signup.Provisioner.activeMembership:
// ACTIVE status on a tenant whose archived_at is null. Keep all three in step.
func tenantMembershipCheck(pool *pgxpool.Pool) auth.MembershipCheckFunc {
	return func(ctx context.Context, userID, tenantID uuid.UUID) (bool, error) {
		var exists bool
		err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM public.tenant_users tu
				  JOIN public.tenants t ON t.id = tu.tenant_id
				 WHERE tu.user_id     = $1
				   AND tu.tenant_id   = $2
				   AND tu.status      = 'ACTIVE'
				   AND t.archived_at IS NULL
			)
		`, userID, tenantID).Scan(&exists)
		if err != nil {
			return false, fmt.Errorf("auth tenant membership check: %w", err)
		}
		return exists, nil
	}
}

// auditSinkEnabled returns true only when the explicit opt-in environment
// variable for the named sink is set to "true". Credential presence alone is
// not sufficient: on the sovereign enterprise profile all external egress is
// off by default and must be consciously enabled. The variable names match
// the public.tenant_setting_key enum values (ENABLE_AUDIT_SINK_*) so that
// operators use the same vocabulary whether configuring via env or DB setting.
func auditSinkEnabled(key string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(key)), "true")
}

func configuredAuditSinks() []auditworker.Sink {
	configured := make([]auditworker.Sink, 0, 6)
	// Each sink requires BOTH an explicit enable flag AND valid credentials.
	// The enable flags default to absent (off), making every external sink
	// opt-in. This satisfies the sovereign-edge zero-egress promise.
	if auditSinkEnabled("ENABLE_AUDIT_SINK_ELK") {
		if url := strings.TrimSpace(os.Getenv("AUDIT_SINK_ELK_URL")); url != "" {
			configured = append(configured, sinks.NewELK(sinks.ELKConfig{
				URL:    url,
				APIKey: strings.TrimSpace(os.Getenv("AUDIT_SINK_ELK_API_KEY")),
			}))
		} else {
			log.Println("WARNING: ENABLE_AUDIT_SINK_ELK=true but AUDIT_SINK_ELK_URL is unset — sink skipped")
		}
	}
	if auditSinkEnabled("ENABLE_AUDIT_SINK_LOKI") {
		if url := strings.TrimSpace(os.Getenv("AUDIT_SINK_LOKI_URL")); url != "" {
			configured = append(configured, sinks.NewLoki(sinks.LokiConfig{URL: url}))
		} else {
			log.Println("WARNING: ENABLE_AUDIT_SINK_LOKI=true but AUDIT_SINK_LOKI_URL is unset — sink skipped")
		}
	}
	if auditSinkEnabled("ENABLE_AUDIT_SINK_DATADOG") {
		if key := strings.TrimSpace(os.Getenv("AUDIT_SINK_DATADOG_API_KEY")); key != "" {
			configured = append(configured, sinks.NewDatadog(sinks.DatadogConfig{
				APIKey: key,
				Site:   strings.TrimSpace(os.Getenv("AUDIT_SINK_DATADOG_SITE")),
			}))
		} else {
			log.Println("WARNING: ENABLE_AUDIT_SINK_DATADOG=true but AUDIT_SINK_DATADOG_API_KEY is unset — sink skipped")
		}
	}
	if auditSinkEnabled("ENABLE_AUDIT_SINK_SPLUNK") {
		url := strings.TrimSpace(os.Getenv("AUDIT_SINK_SPLUNK_HEC_URL"))
		token := strings.TrimSpace(os.Getenv("AUDIT_SINK_SPLUNK_HEC_TOKEN"))
		if url != "" && token != "" {
			configured = append(configured, sinks.NewSplunk(sinks.SplunkConfig{
				URL:   url,
				Token: token,
			}))
		} else {
			log.Println("WARNING: ENABLE_AUDIT_SINK_SPLUNK=true but AUDIT_SINK_SPLUNK_HEC_URL or AUDIT_SINK_SPLUNK_HEC_TOKEN is unset — sink skipped")
		}
	}
	if auditSinkEnabled("ENABLE_AUDIT_SINK_SENTRY") {
		if dsn := strings.TrimSpace(os.Getenv("SENTRY_DSN")); dsn != "" {
			configured = append(configured, sinks.NewSentry(sinks.SentryConfig{DSN: dsn}))
		} else {
			log.Println("WARNING: ENABLE_AUDIT_SINK_SENTRY=true but SENTRY_DSN is unset — sink skipped")
		}
	}
	if auditSinkEnabled("ENABLE_AUDIT_SINK_LANGFUSE") {
		host := strings.TrimSpace(os.Getenv("LANGFUSE_HOST"))
		pub := strings.TrimSpace(os.Getenv("LANGFUSE_PUBLIC_KEY"))
		sec := strings.TrimSpace(os.Getenv("LANGFUSE_SECRET_KEY"))
		if host != "" && pub != "" && sec != "" {
			configured = append(configured, sinks.NewLangfuse(sinks.LangfuseConfig{
				Host:      host,
				PublicKey: pub,
				SecretKey: sec,
			}))
		} else {
			log.Println("WARNING: ENABLE_AUDIT_SINK_LANGFUSE=true but LANGFUSE_HOST, LANGFUSE_PUBLIC_KEY, or LANGFUSE_SECRET_KEY is unset — sink skipped")
		}
	}
	return configured
}

func loadStorageConfigFromEnv() (storageRuntimeConfig, error) {
	cfg := storageRuntimeConfig{
		Client: storage.Config{
			Endpoint:  strings.TrimSpace(os.Getenv("S3_ENDPOINT")),
			AccessKey: strings.TrimSpace(os.Getenv("S3_ACCESS_KEY")),
			SecretKey: strings.TrimSpace(os.Getenv("S3_SECRET_KEY")),
			Region:    strings.TrimSpace(os.Getenv("S3_REGION")),
		},
		FilesBucket: strings.TrimSpace(os.Getenv("S3_BUCKET_FILES")),
	}

	missing := make([]string, 0, 5)
	if cfg.Client.Endpoint == "" {
		missing = append(missing, "S3_ENDPOINT")
	}
	if cfg.Client.AccessKey == "" {
		missing = append(missing, "S3_ACCESS_KEY")
	}
	if cfg.Client.SecretKey == "" {
		missing = append(missing, "S3_SECRET_KEY")
	}
	if cfg.Client.Region == "" {
		missing = append(missing, "S3_REGION")
	}
	if cfg.FilesBucket == "" {
		missing = append(missing, "S3_BUCKET_FILES")
	}
	if len(missing) > 0 {
		return storageRuntimeConfig{}, fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

// envOr returns the trimmed value of the named env var, or fallback if unset/empty.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// parseIntEnv parses a base-10 integer from an env var; returns fallback on parse failure or absence.
func parseIntEnv(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return fallback
	}
	return n
}

// parseDurationEnv parses a Go duration string from an env var; returns fallback on failure.
func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// buildAgentEngine wires the real host<->agent-server control channel
// (issue #305) when its deployment-specific paths are all configured via
// env, falling back to agenttask.NotConfiguredEngine{} otherwise — which
// now fails every submitted task visibly (see agenttask.Service.CreateTask)
// rather than leaving it queued forever with no signal. When falling back,
// this logs a WARN naming every missing HIVE_AGENT_ENGINE_* var so an
// operator can act on it instead of discovering the gap from a support
// ticket. The real SandboxEngine (apps/agent-engine/engineapi) execs
// Apptainer directly, which requires an Apptainer install and a built SIF
// on whatever host runs this process — not true of every control-plane
// deployment yet (tracked separately: "Live Apptainer validation of
// agent-engine on x86-64 host"). egressSvc is reused in-process rather than
// calling apps/agent-engine/internal/egressclient's HTTP round-trip, since
// the real Engine now runs inside this same control-plane process.
// buildAgentEngine's second return value is the same *agentengine.Engine as
// a agenttask.StatusChecker (nil when unconfigured) — the seam
// cmd/server/main.go's poller wiring uses, since NotConfiguredEngine has no
// Status method to poll. The third is that same value as an
// agenttask.EventSource for the event-sync loop (nil only when the engine is
// unconfigured or, on the socket arm, when the daemon failed its boot health
// probe — a missed events pull degrades safely and the daemon coming up later
// still serves every task it registered).
func buildAgentEngine(egressSvc *egress.Service) (agenttask.Engine, agenttask.StatusChecker, agenttask.EventSource) {
	// Issue #780: on any deployment where this process runs in a container
	// (every compose topology this repo ships), it cannot exec Apptainer at
	// all — musl base, no /dev/fuse, no CAP_SYS_ADMIN — and granting it
	// those privileges would put sandbox-escape-class capability on the one
	// process holding the payment and database secrets. When
	// HIVE_AGENT_ENGINE_SOCKET is set, launches go to the unprivileged host
	// daemon (apps/agent-engine/cmd/agent-engine -serve) over that socket
	// instead, and none of the local paths below are read.
	if socketPath := os.Getenv("HIVE_AGENT_ENGINE_SOCKET"); socketPath != "" {
		remote := agentengine.NewRemote(socketPath, os.Getenv("CONTROL_PLANE_INTERNAL_TOKEN"))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := remote.Health(ctx); err != nil {
			// Not fatal: the daemon may still be starting, and a task that
			// arrives before it is up fails with this same error rather than
			// silently queueing forever.
			log.Printf("control-plane: WARN agent-engine daemon at %s did not answer /health: %v", socketPath, err)
		} else {
			log.Printf("control-plane: agent-engine daemon reachable at %s", socketPath)
		}
		return remote, remote, remote
	}

	sifPath := os.Getenv("HIVE_AGENT_ENGINE_SIF_PATH")
	packsDir := os.Getenv("HIVE_AGENT_ENGINE_PACKS_DIR")
	workspaceRoot := os.Getenv("HIVE_AGENT_ENGINE_WORKSPACE_ROOT")
	runDir := os.Getenv("HIVE_AGENT_ENGINE_RUN_DIR")
	profileIDRaw := os.Getenv("HIVE_AGENT_ENGINE_PROFILE_ID")
	if egressSvc == nil || sifPath == "" || packsDir == "" || workspaceRoot == "" || runDir == "" || profileIDRaw == "" {
		var missing []string
		if egressSvc == nil {
			missing = append(missing, "egress service (requires a live DB pool)")
		}
		if sifPath == "" {
			missing = append(missing, "HIVE_AGENT_ENGINE_SIF_PATH")
		}
		if packsDir == "" {
			missing = append(missing, "HIVE_AGENT_ENGINE_PACKS_DIR")
		}
		if workspaceRoot == "" {
			missing = append(missing, "HIVE_AGENT_ENGINE_WORKSPACE_ROOT")
		}
		if runDir == "" {
			missing = append(missing, "HIVE_AGENT_ENGINE_RUN_DIR")
		}
		if profileIDRaw == "" {
			missing = append(missing, "HIVE_AGENT_ENGINE_PROFILE_ID")
		}
		log.Printf("control-plane: WARN agent engine not configured, every agent task submitted will fail immediately (never runs) — missing: %s", strings.Join(missing, ", "))
		return agenttask.NotConfiguredEngine{}, nil, nil
	}
	profileID, err := uuid.Parse(profileIDRaw)
	if err != nil {
		log.Printf("control-plane: HIVE_AGENT_ENGINE_PROFILE_ID invalid, agent-engine stays unconfigured: %v", err)
		return agenttask.NotConfiguredEngine{}, nil, nil
	}

	sandbox := engineapi.New(engineapi.Config{
		SIFPath:       sifPath,
		PacksDir:      packsDir,
		WorkspaceRoot: workspaceRoot,
		RunDir:        runDir,
		ResolveEgressHosts: func(ctx context.Context, tenantID, userID uuid.UUID) ([]string, error) {
			policy, err := egressSvc.Effective(ctx, tenantID, userID)
			if err != nil {
				return nil, err
			}
			return policy.AllowedHosts, nil
		},
		AgentProfileID: profileID,
		SessionAPIKey:  os.Getenv("HIVE_AGENT_ENGINE_SESSION_API_KEY"),
		// Issue #308 noisy-neighbour controls: how many sessions a tenant or
		// user may run at once, and what each one may consume. Zero values
		// fall back to engineapi's own defaults.
		QuotaTenantConcurrency: parseIntEnv("HIVE_QUOTA_TENANT_CONCURRENCY", 4),
		QuotaUserConcurrency:   parseIntEnv("HIVE_QUOTA_USER_CONCURRENCY", 2),
		MemoryLimit:            envOr("HIVE_SANDBOX_MEMORY_LIMIT", "4G"),
		CPULimit:               envOr("HIVE_SANDBOX_CPU_LIMIT", "2"),
		PidsLimit:              parseIntEnv("HIVE_SANDBOX_PIDS_LIMIT", 512),
	})
	real := agentengine.New(sandbox)
	return real, real, real
}

// dbReadyFunc builds the callback platformhttp.RouterConfig.DBReady calls on
// every /health request.
//
// pool != nil is the same condition that gates every DB-backed handler, so
// /health cannot report ok while those routes are absent (issue #816).
// resolveHealth.Degraded() is the runtime half: a pgxpool.Pool is never nil
// again after a successful boot, even when every checkout is timing out, so
// the boot-time condition alone answers 200 straight through the outage this
// exists to catch (issue #836).
//
// Extracted from the RouterConfig literal so both halves are reachable from a
// test. Inline, deleting the resolveHealth term left the whole suite green:
// the tracker and the callback plumbing were each covered, and their
// composition here was not.
func dbReadyFunc(pool *pgxpool.Pool, resolveHealth *platformdb.ResolveHealth) func() bool {
	return func() bool {
		if pool == nil {
			return false
		}
		return resolveHealth == nil || !resolveHealth.Degraded()
	}
}
