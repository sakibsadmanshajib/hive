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

	"github.com/hivegpt/hive/apps/control-plane/internal/accounting"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
	"github.com/hivegpt/hive/apps/control-plane/internal/catalog"
	"github.com/hivegpt/hive/apps/control-plane/internal/ledger"
	"github.com/hivegpt/hive/apps/control-plane/internal/platform/config"
	platformdb "github.com/hivegpt/hive/apps/control-plane/internal/platform/db"
	platformhttp "github.com/hivegpt/hive/apps/control-plane/internal/platform/http"
	platformredis "github.com/hivegpt/hive/apps/control-plane/internal/platform/redis"
	"github.com/hivegpt/hive/apps/control-plane/internal/profiles"
	"github.com/hivegpt/hive/apps/control-plane/internal/routing"
	"github.com/hivegpt/hive/apps/control-plane/internal/usage"
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
	var catalogHandler *catalog.Handler
	var ledgerHandler *ledger.Handler
	var profilesHandler *profiles.Handler
	var routingHandler *routing.Handler
	var usageHandler *usage.Handler
	if pool != nil {
		if cfg.RedisURL != "" {
			redisClient := platformredis.NewClient(cfg.RedisURL)
			if err := platformredis.Ping(ctx, redisClient); err != nil {
				log.Printf("WARNING: redis not available at startup: %v", err)
				_ = redisClient.Close()
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

		accountingRepo := accounting.NewPgxRepository(pool)
		accountingSvc := accounting.NewService(accountingRepo, ledgerSvc, usageSvc)
		accountingHandler = accounting.NewHandler(accountingSvc, accountsSvc)
	} else {
		log.Println("WARNING: accounts routes not available — database pool not ready")
	}

	router := platformhttp.NewRouter(platformhttp.RouterConfig{
		AuthMiddleware:    authMiddleware,
		AccountsHandler:   accountsHandler,
		AccountingHandler: accountingHandler,
		CatalogHandler:    catalogHandler,
		LedgerHandler:     ledgerHandler,
		ProfilesHandler:   profilesHandler,
		RoutingHandler:    routingHandler,
		UsageHandler:      usageHandler,
	})

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
