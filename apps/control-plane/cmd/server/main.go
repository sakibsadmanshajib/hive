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

	"github.com/hivegpt/hive/apps/control-plane/internal/platform/config"
	platformdb "github.com/hivegpt/hive/apps/control-plane/internal/platform/db"
	platformhttp "github.com/hivegpt/hive/apps/control-plane/internal/platform/http"
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

	router := platformhttp.NewRouter()

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
