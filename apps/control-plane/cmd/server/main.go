package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounting"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
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
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/litellmconfig"
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
	platformredis "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/redis"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/profiles"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/providers"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/routing"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/signup"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/signupguard"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/sovereign"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/spendalerts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenant/settings"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenants"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usage"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/waldrainer"
	"github.com/sakibsadmanshajib/hive/packages/storage"
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
func (a *accountsAccessChecker) IsWorkspaceMember(ctx context.Context, userID, workspaceID uuid.UUID) (bool, error) {
	memberships, err := a.repo.ListMembershipsByUserID(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("invoices access: list memberships: %w", err)
	}
	for _, m := range memberships {
		if m.AccountID == workspaceID {
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// runCtx is the process-lifetime context for background goroutines
	// (audit sink worker, WAL drainer, hash-chain verifier). It is cancelled
	// on shutdown so those goroutines unwind cleanly instead of being killed
	// mid-write, which would risk partial WAL flushes and orphan outbox rows.
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	// Open the database pool. A missing SUPABASE_DB_URL is treated as a
	// non-fatal warning at startup so the service can still respond to /health
	// in environments where the DB URL is not yet provisioned.
	pool, dbErr := platformdb.Open(ctx, cfg.SupabaseDBURL)
	if dbErr != nil {
		log.Printf("WARNING: database not available at startup: %v", dbErr)
	} else {
		defer pool.Close()
		log.Println("database pool ready")
	}

	// Build auth client and middleware.
	authClient := auth.NewClient(cfg.SupabaseURL, cfg.SupabaseAnonKey)
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
		accountsHandler = accounts.NewHandler(accountsSvc)

		catalogRepo := catalog.NewPgxRepository(pool)
		catalogSvc := catalog.NewService(catalogRepo)
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

		routingRepo := routing.NewPgxRepository(pool)
		routingSvc = routing.NewService(routingRepo)
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
		apikeysHandler = apikeys.NewHandler(apikeysSvc, accountsSvc)

		accountingRepo := accounting.NewPgxRepository(pool)
		// Postgres advisory locker serializes the credit-reservation critical
		// section across all control-plane instances, preventing the TOCTOU
		// credit double-spend (issue #106). Single-instance in-process locking
		// is the NewService default; this upgrades it to be cross-process safe.
		accountingSvc = accounting.NewService(accountingRepo, ledgerSvc, usageSvc, apikeysSvc).
			WithAccountLocker(accounting.NewPgxAccountLocker(pool))
		accountingHandler = accounting.NewHandler(accountingSvc, accountsSvc)

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

		// Phase 18 — wire role service into the apikeys handler so the admin
		// overlay is reflected in Actor.IsAdmin during PermAPIKeysWrite checks.
		// Without it, platform admins are silently denied by policy.Can.
		if apikeysHandler != nil {
			apikeysHandler = apikeysHandler.WithRoleService(roleSvc)
		}

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

		missingPhase19 := make([]string, 0, 4)
		if owuiBaseURL == "" {
			missingPhase19 = append(missingPhase19, "OWUI_BASE_URL")
		}
		if owuiAdminToken == "" {
			missingPhase19 = append(missingPhase19, "OWUI_ADMIN_TOKEN")
		}
		if signupSecret == "" {
			missingPhase19 = append(missingPhase19, "SUPABASE_WEBHOOK_SECRET")
		}
		if supabaseServiceRoleKey == "" {
			missingPhase19 = append(missingPhase19, "SUPABASE_SERVICE_ROLE_KEY")
		}
		if len(missingPhase19) > 0 {
			// Phase 19 identity (signup webhook + tenant switch) is opt-in.
			// When env vars are absent — typical for CI smoke runs that do
			// not exercise the Supabase signup path — log and skip the
			// wiring rather than fatal, so other unrelated startup paths
			// (health, billing, catalog) still come up healthy. Production
			// deployments are expected to set every variable in this list;
			// the resulting warning is loud enough for operators to catch.
			log.Printf("WARNING: phase-19 identity wiring skipped (missing env: %s)", strings.Join(missingPhase19, ", "))
		} else {
			// SUPABASE_SERVICE_ROLE_KEY is read at startup so production
			// deployments surface misconfiguration early, but the tenant
			// switch handler uses the already-authenticated pool
			// connection (which carries service-role privilege) to update
			// auth.users metadata, so the key is not threaded into the
			// handler today. Underscore the var to keep the contract
			// explicit until later tasks consume it.
			_ = supabaseServiceRoleKey

			owuiClient = owui.New(owui.Config{
				BaseURL:    owuiBaseURL,
				AdminToken: owuiAdminToken,
			})

			signupResolver := signup.NewResolver(signup.ResolverDeps{
				InviteLookup: signupLookupInvite(pool),
				DomainLookup: signupLookupDomain(pool),
			})

			signupWebhook = signup.NewWebhook(signup.WebhookDeps{
				Pool:        pool,
				Resolver:    signupResolver,
				EnsureGroup: owuiClient.EnsureGroup,
				AddUser:     owuiClient.AddUserToGroup,
				Audit:       auditLogger,
				// Disposable-domain backstop (issue #116) for scripted signups
				// that hit Supabase directly and bypass the web-console precheck.
				DisposableCheck: disposableBlocklist.IsDisposableEmail,
				SharedSecret:    signupSecret,
			})

			tenantsHandler = tenants.NewHandler(tenants.Deps{Pool: pool, Audit: auditLogger})
			log.Println("phase-19 identity wiring ready (signup webhook + tenants router)")
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

	// Build Prometheus metrics registry before the router so /metrics and
	// instrumentation middleware are wired in together.
	metricsRegistry, promRegistry := metrics.NewRegistry()

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
	if pool != nil {
		settingsResolver := settings.NewResolver(pool, 30*time.Second)
		featureGateHandler = featuregate.NewHandler(settingsResolver)
	}

	// Issue #308 — egress policy single source of truth. Admin CRUD is
	// owner-gated via roleSvc.IsWorkspaceOwner (constructed above, in scope
	// whenever pool != nil). Neither the server-side OpenHands allowed_hosts
	// consumer nor the desktop firewall rule generator is wired here.
	var egressPolicyHandler *egress.Handler
	if pool != nil && roleSvc != nil {
		egressRepo := egress.NewPgxRepository(pool)
		egressSvc := egress.NewService(egressRepo, roleSvc)
		egressPolicyHandler = egress.NewHandler(egressSvc)
	}

	router := platformhttp.NewRouter(platformhttp.RouterConfig{
		AuthMiddleware:      authMiddleware,
		AccountsHandler:     accountsHandler,
		IdentityHandler:     identityHandler,
		AccountingHandler:   accountingHandler,
		APIKeysHandler:      apikeysHandler,
		BudgetsHandler:      budgetsHandler,
		CatalogHandler:      catalogHandler,
		LedgerHandler:       ledgerHandler,
		PaymentsHandler:     paymentsHandler,
		ProfilesHandler:     profilesHandler,
		ProvidersRouter:     providersHandler,
		LiteLLMSyncHandler:  litellmSyncHandler,
		FeatureGateHandler:  featureGateHandler,
		EgressPolicyHandler: egressPolicyHandler,
		RoutingHandler:      routingHandler,
		UsageHandler:        usageHandler,
		MetricsRegistry:     metricsRegistry,
		PrometheusRegistry:  promRegistry,
		Mux:                 routerMux,
		InternalToken:       cfg.InternalToken,
		RoleSvc:             roleSvc,
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

	// Phase 14 — register credit grant routes. Admin surface gated via
	// RequirePlatformAdmin (provider-blind 401/403 sanitised JSON); self
	// surface gated via plain auth middleware.
	if grantsHandler != nil && roleSvc != nil && authzMW.Initialized() {
		adminGate := authzMW.RequirePermission(authz.PermPlatformAdmin)(grantsHandler.AdminMux())
		protectedAdminGrants := authMiddleware.Require(adminGate)
		routerMux.Handle("/v1/admin/credit-grants", protectedAdminGrants)
		routerMux.Handle("/v1/admin/credit-grants/", protectedAdminGrants)

		protectedSelfGrants := authMiddleware.Require(grantsHandler.SelfMux())
		routerMux.Handle("/v1/credit-grants/me", protectedSelfGrants)
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

// signupLookupInvite returns a signup.LookupFunc that resolves an invite
// token to its target tenant. tenant_invites is provisioned by Plan 03;
// until then the query simply returns ErrNoMatch, gating the resolver to
// its domain-mapping fallback.
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

func signupLookupInvite(pool *pgxpool.Pool) signup.LookupFunc {
	return func(ctx context.Context, token string) (uuid.UUID, error) {
		var id uuid.UUID
		err := pool.QueryRow(ctx,
			`SELECT tenant_id FROM public.tenant_invites
			  WHERE token=$1 AND consumed_at IS NULL AND expires_at > now()`,
			token).Scan(&id)
		if err != nil {
			// Only "no eligible row" collapses to ErrNoMatch; transient
			// DB failures (connection reset, deadline exceeded) must
			// surface so the webhook returns 500 and Supabase retries.
			if errors.Is(err, pgx.ErrNoRows) {
				return uuid.Nil, signup.ErrNoMatch
			}
			return uuid.Nil, fmt.Errorf("signup invite lookup: %w", err)
		}
		return id, nil
	}
}

// signupLookupDomain returns a signup.LookupFunc that maps an email
// domain to its tenant via tenant_email_domains. As with the invite
// table, the schema lands in Plan 03; the function is safe to call now
// because a missing relation collapses to ErrNoMatch.
func signupLookupDomain(pool *pgxpool.Pool) signup.LookupFunc {
	return func(ctx context.Context, domain string) (uuid.UUID, error) {
		var id uuid.UUID
		err := pool.QueryRow(ctx,
			`SELECT tenant_id FROM public.tenant_email_domains WHERE domain=$1`,
			domain).Scan(&id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return uuid.Nil, signup.ErrNoMatch
			}
			return uuid.Nil, fmt.Errorf("signup domain lookup: %w", err)
		}
		return id, nil
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
