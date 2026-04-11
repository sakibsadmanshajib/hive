package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounting"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/apikeys"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
	"github.com/hivegpt/hive/apps/control-plane/internal/batchstore"
	"github.com/hivegpt/hive/apps/control-plane/internal/budgets"
	"github.com/hivegpt/hive/apps/control-plane/internal/catalog"
	"github.com/hivegpt/hive/apps/control-plane/internal/filestore"
	"github.com/hivegpt/hive/apps/control-plane/internal/ledger"
	"github.com/hivegpt/hive/apps/control-plane/internal/payments"
	bkashRail "github.com/hivegpt/hive/apps/control-plane/internal/payments/bkash"
	sslcommerzRail "github.com/hivegpt/hive/apps/control-plane/internal/payments/sslcommerz"
	stripeRail "github.com/hivegpt/hive/apps/control-plane/internal/payments/stripe"
	"github.com/hivegpt/hive/apps/control-plane/internal/platform/config"
	platformdb "github.com/hivegpt/hive/apps/control-plane/internal/platform/db"
	platformhttp "github.com/hivegpt/hive/apps/control-plane/internal/platform/http"
	"github.com/hivegpt/hive/apps/control-plane/internal/platform/metrics"
	platformredis "github.com/hivegpt/hive/apps/control-plane/internal/platform/redis"
	"github.com/hivegpt/hive/apps/control-plane/internal/profiles"
	"github.com/hivegpt/hive/apps/control-plane/internal/routing"
	"github.com/hivegpt/hive/apps/control-plane/internal/usage"
	goredis "github.com/redis/go-redis/v9"
)

// accountsResolverAdapter adapts accounts.Service to the payments.AccountResolver interface.
// It extracts the viewer from context (set by auth middleware) and resolves the current account.
type accountsResolverAdapter struct {
	svc *accounts.Service
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
	return viewerCtx.CurrentAccount.ID, nil
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
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
	var catalogHandler *catalog.Handler
	var ledgerHandler *ledger.Handler
	var profilesHandler *profiles.Handler
	var routingHandler *routing.Handler
	var usageHandler *usage.Handler
	var redisClient *goredis.Client
	// Hoisted so the payments wiring block below can reference them.
	var accountsSvc *accounts.Service
	var ledgerSvc *ledger.Service
	var profilesSvc *profiles.Service
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

		routingRepo := routing.NewPgxRepository(pool)
		routingSvc := routing.NewService(routingRepo)
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
		accountingSvc := accounting.NewService(accountingRepo, ledgerSvc, usageSvc, apikeysSvc)
		accountingHandler = accounting.NewHandler(accountingSvc, accountsSvc)

		budgetsRepo := budgets.NewPgxRepository(pool)
		emailNotifier := budgets.NewLogNotifier(slog.Default())
		budgetsSvc := budgets.NewService(budgetsRepo, emailNotifier)
		budgetsHandler = budgets.NewHandler(budgetsSvc, accountsSvc)
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
		paymentsSvc := payments.NewService(paymentsRepo, ledgerSvc, profilesSvc, fxSvc, rails)
		paymentsHandler = payments.NewHandler(paymentsSvc, &accountsResolverAdapter{svc: accountsSvc})

		// Background goroutine: confirm pending BD payments every 60 seconds.
		// BD rails require a 3-minute confirming delay before ledger grant.
		go func() {
			ticker := time.NewTicker(60 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					confirmed, err := paymentsSvc.ConfirmPendingBDPayments(context.Background())
					if err != nil {
						log.Printf("payments: error confirming BD payments: %v", err)
					} else if confirmed > 0 {
						log.Printf("payments: confirmed %d pending BD payment(s)", confirmed)
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Build Prometheus metrics registry before the router so /metrics and
	// instrumentation middleware are wired in together.
	metricsRegistry, promRegistry := metrics.NewRegistry()

	// Create the mux upfront so filestore.RegisterRoutes (which requires *http.ServeMux)
	// can register routes on it before the instrumentation wrapper is applied.
	routerMux := http.NewServeMux()

	router := platformhttp.NewRouter(platformhttp.RouterConfig{
		AuthMiddleware:     authMiddleware,
		AccountsHandler:    accountsHandler,
		AccountingHandler:  accountingHandler,
		APIKeysHandler:     apikeysHandler,
		BudgetsHandler:     budgetsHandler,
		CatalogHandler:     catalogHandler,
		LedgerHandler:      ledgerHandler,
		PaymentsHandler:    paymentsHandler,
		ProfilesHandler:    profilesHandler,
		RoutingHandler:     routingHandler,
		UsageHandler:       usageHandler,
		MetricsRegistry:    metricsRegistry,
		PrometheusRegistry: promRegistry,
		Mux:                routerMux,
	})

	// Wire filestore internal endpoints if the database pool is available.
	if pool != nil {
		filestoreRepo, err := filestore.NewRepository(pool)
		if err != nil {
			log.Printf("WARNING: filestore schema setup failed: %v", err)
		} else {
			filestoreSvc := filestore.NewService(filestoreRepo)
			filestore.RegisterRoutes(routerMux, filestoreSvc)
			log.Println("filestore routes registered")

			// Start Asynq batch polling worker if Redis is available.
			if cfg.RedisURL != "" {
				redisOpt, parseErr := asynq.ParseRedisURI(cfg.RedisURL)
				if parseErr != nil {
					log.Printf("WARNING: could not parse Redis URL for asynq worker: %v", parseErr)
				} else {
					batchWorker := batchstore.NewBatchWorker(
						filestoreSvc,
						resolveLiteLLMBaseURL(),
						resolveLiteLLMMasterKey(),
						nil, // StorageUploader: nil until S3 client is wired into control-plane
						resolveBucketFiles(),
					)
					asynqMux := asynq.NewServeMux()
					asynqMux.HandleFunc(batchstore.TypeBatchPoll, batchWorker.HandleBatchPoll)

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
		}
	}

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

func resolveBucketFiles() string {
	if b := os.Getenv("S3_BUCKET_FILES"); b != "" {
		return b
	}
	return "hive-files"
}
