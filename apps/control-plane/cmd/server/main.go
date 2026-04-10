package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounting"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/apikeys"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
	"github.com/hivegpt/hive/apps/control-plane/internal/batchstore"
	"github.com/hivegpt/hive/apps/control-plane/internal/catalog"
	"github.com/hivegpt/hive/apps/control-plane/internal/filestore"
	"github.com/hivegpt/hive/apps/control-plane/internal/ledger"
	"github.com/hivegpt/hive/apps/control-plane/internal/platform/config"
	platformdb "github.com/hivegpt/hive/apps/control-plane/internal/platform/db"
	platformhttp "github.com/hivegpt/hive/apps/control-plane/internal/platform/http"
	platformredis "github.com/hivegpt/hive/apps/control-plane/internal/platform/redis"
	"github.com/hivegpt/hive/apps/control-plane/internal/profiles"
	"github.com/hivegpt/hive/apps/control-plane/internal/routing"
	"github.com/hivegpt/hive/apps/control-plane/internal/usage"
	goredis "github.com/redis/go-redis/v9"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
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
	var catalogHandler *catalog.Handler
	var ledgerHandler *ledger.Handler
	var profilesHandler *profiles.Handler
	var routingHandler *routing.Handler
	var usageHandler *usage.Handler
	var redisClient *goredis.Client
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
		accountsSvc := accounts.NewService(accountsRepo)
		accountsHandler = accounts.NewHandler(accountsSvc)

		catalogRepo := catalog.NewPgxRepository(pool)
		catalogSvc := catalog.NewService(catalogRepo)
		catalogHandler = catalog.NewHandler(catalogSvc)

		routingRepo := routing.NewPgxRepository(pool)
		routingSvc := routing.NewService(routingRepo)
		routingHandler = routing.NewHandler(routingSvc)

		ledgerRepo := ledger.NewPgxRepository(pool)
		ledgerSvc := ledger.NewService(ledgerRepo)
		ledgerHandler = ledger.NewHandler(ledgerSvc, accountsSvc)

		profilesRepo := profiles.NewPgxRepository(pool)
		profilesSvc := profiles.NewService(profilesRepo)
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
	} else {
		log.Println("WARNING: accounts routes not available — database pool not ready")
	}

	router := platformhttp.NewRouter(platformhttp.RouterConfig{
		AuthMiddleware:    authMiddleware,
		AccountsHandler:   accountsHandler,
		AccountingHandler: accountingHandler,
		APIKeysHandler:    apikeysHandler,
		CatalogHandler:    catalogHandler,
		LedgerHandler:     ledgerHandler,
		ProfilesHandler:   profilesHandler,
		RoutingHandler:    routingHandler,
		UsageHandler:      usageHandler,
	})

	// Wire filestore internal endpoints if the database pool is available.
	if pool != nil {
		filestoreRepo, err := filestore.NewRepository(pool)
		if err != nil {
			log.Printf("WARNING: filestore schema setup failed: %v", err)
		} else {
			filestoreSvc := filestore.NewService(filestoreRepo)
			filestore.RegisterRoutes(router, filestoreSvc)
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
