package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/hivegpt/hive/apps/edge-api/docs"
	"github.com/hivegpt/hive/apps/edge-api/internal/audio"
	"github.com/hivegpt/hive/apps/edge-api/internal/authz"
	"github.com/hivegpt/hive/apps/edge-api/internal/batches"
	"github.com/hivegpt/hive/apps/edge-api/internal/catalog"
	apierrors "github.com/hivegpt/hive/apps/edge-api/internal/errors"
	"github.com/hivegpt/hive/apps/edge-api/internal/files"
	"github.com/hivegpt/hive/apps/edge-api/internal/images"
	"github.com/hivegpt/hive/apps/edge-api/internal/inference"
	"github.com/hivegpt/hive/apps/edge-api/internal/limits"
	"github.com/hivegpt/hive/apps/edge-api/internal/matrix"
	"github.com/hivegpt/hive/apps/edge-api/internal/middleware"
	"github.com/hivegpt/hive/apps/edge-api/internal/proxy"
	"github.com/hivegpt/hive/packages/storage"
	"github.com/redis/go-redis/v9"
)

type storageConfig struct {
	Endpoint     string
	AccessKey    string
	SecretKey    string
	Region       string
	FilesBucket  string
	ImagesBucket string
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	matrixPath := resolveMatrixPath()
	specPath := resolveSpecPath()

	// Load support matrix
	m, err := matrix.LoadMatrix(matrixPath)
	if err != nil {
		log.Fatalf("failed to load support matrix: %v", err)
	}
	log.Printf("Loaded support matrix: %d endpoints", len(m.Endpoints))

	catalogClient := catalog.NewClient(resolveControlPlaneBaseURL())

	// Initialize authz
	authzClient, err := authz.NewClient(resolveControlPlaneBaseURL(), resolveRedisURL())
	if err != nil {
		log.Fatalf("failed to initialize authz client: %v", err)
	}
	limiter, err := authz.NewLimiter(resolveRedisURL())
	if err != nil {
		log.Fatalf("failed to initialize authz limiter: %v", err)
	}
	authorizer := authz.NewAuthorizer(authzClient, limiter)

	// Initialize Prometheus metrics registry for edge-api.
	edgeMetrics, promRegistry := proxy.NewEdgeMetrics()

	// Create the main mux
	mux := http.NewServeMux()

	// Infrastructure routes (no unsupported middleware)
	mux.HandleFunc("/health", handleHealth)

	// Prometheus metrics endpoint — served from the custom registry (not DefaultRegistry).
	mux.Handle("/metrics", proxy.MetricsHandler(promRegistry))

	// Swagger docs (no unsupported middleware)
	swaggerHandler := docs.SwaggerHandler(specPath)
	mux.Handle("/docs/", swaggerHandler)

	// Inference routes
	routingClient := inference.NewRoutingClient(resolveControlPlaneBaseURL())
	accountingClient := inference.NewAccountingClient(resolveControlPlaneBaseURL())
	litellmClient := inference.NewLiteLLMClient(resolveLiteLLMBaseURL(), resolveLiteLLMMasterKey())
	orchestrator := inference.NewOrchestrator(authorizer, routingClient, accountingClient, litellmClient)
	inferenceHandler := inference.NewHandler(orchestrator)

	mux.Handle("/v1/chat/completions", inferenceHandler)
	mux.Handle("/v1/completions", inferenceHandler)
	mux.Handle("/v1/responses", inferenceHandler)
	mux.Handle("/v1/embeddings", inferenceHandler)

	storageCfg, err := loadStorageConfigFromEnv()
	if err != nil {
		log.Fatalf("storage unavailable: %v", err)
	}
	storageClient, err := storage.NewS3Client(storage.Config{
		Endpoint:  storageCfg.Endpoint,
		AccessKey: storageCfg.AccessKey,
		SecretKey: storageCfg.SecretKey,
		Region:    storageCfg.Region,
	})
	if err != nil {
		log.Fatalf("storage unavailable: %v", err)
	}

	imagesAuthorizer := images.NewAuthorizerAdapter(authorizer)
	imagesRouting := images.NewRoutingAdapter(routingClient)
	imagesAccounting := images.NewAccountingAdapter(accountingClient)
	imagesHandler := images.NewHandler(
		imagesAuthorizer,
		imagesRouting,
		imagesAccounting,
		resolveLiteLLMBaseURL(),
		resolveLiteLLMMasterKey(),
		storageClient,
		storageCfg.ImagesBucket,
	)

	audioAuthorizer := audio.NewAuthorizerAdapter(authorizer)
	audioRouting := audio.NewRoutingAdapter(routingClient)
	audioAccounting := audio.NewAccountingAdapter(accountingClient)
	audioHandler := audio.NewHandler(
		audioAuthorizer,
		audioRouting,
		audioAccounting,
		resolveLiteLLMBaseURL(),
		resolveLiteLLMMasterKey(),
	)

	filestoreClient := files.NewFilestoreClient(resolveControlPlaneBaseURL())
	filesAuthorizer := files.NewAuthorizerAdapter(authorizer)
	filesHandler := files.NewHandler(filesAuthorizer, storageClient, filestoreClient, storageCfg.FilesBucket)

	batchClient := batches.NewBatchClient(resolveControlPlaneBaseURL())
	batchesAuthorizer := batches.NewAuthorizerAdapter(authorizer)
	batchesFileClient := batches.NewFilestoreAdapter(filestoreClient)
	batchesAccounting := batches.NewAccountingAdapter(accountingClient)
	batchesHandler := batches.NewHandler(batchesAuthorizer, batchClient, batchesFileClient, storageClient, batchesAccounting, storageCfg.FilesBucket)

	registerMediaFileBatchRoutes(mux, imagesHandler, audioHandler, filesHandler, batchesHandler)

	log.Printf("S3 storage enabled: images=%s, files=%s", storageCfg.ImagesBucket, storageCfg.FilesBucket)

	// API routes
	mux.Handle("/v1/models", handleModels(catalogClient, authorizer))
	mux.Handle("/catalog/models", handleCatalogModels(catalogClient))

	// Apply middleware: CompatHeaders (outermost) -> Metrics -> BudgetGate -> UnsupportedEndpoint (inner)
	//
	// Phase 14 — BudgetGate sits between metrics and unsupported-endpoint detection.
	// It pulls workspace identity by hashing the bearer token through the authz
	// resolver, then enforces the hard-cap stored in Redis (key written by the
	// control-plane budgets service on every Set/DeleteBudget call). Soft-cap
	// crossings are non-blocking but emit `budget_soft_cap_crossed_total`.
	budgetGate, err := buildBudgetGate(authzClient)
	if err != nil {
		log.Fatalf("failed to initialize budget gate: %v", err)
	}

	var handler http.Handler = mux
	handler = middleware.UnsupportedEndpointMiddleware(m)(handler)
	handler = budgetGate.Wrap(handler)
	handler = proxy.InstrumentHandler(edgeMetrics, handler)
	handler = middleware.CompatHeaders()(handler)

	log.Printf("edge-api listening on :%s", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func loadStorageConfigFromEnv() (storageConfig, error) {
	endpoint, err := requireStorageEnv("S3_ENDPOINT")
	if err != nil {
		return storageConfig{}, err
	}
	accessKey, err := requireStorageEnv("S3_ACCESS_KEY")
	if err != nil {
		return storageConfig{}, err
	}
	secretKey, err := requireStorageEnv("S3_SECRET_KEY")
	if err != nil {
		return storageConfig{}, err
	}
	region, err := requireStorageEnv("S3_REGION")
	if err != nil {
		return storageConfig{}, err
	}
	filesBucket, err := requireStorageEnv("S3_BUCKET_FILES")
	if err != nil {
		return storageConfig{}, err
	}
	imagesBucket, err := requireStorageEnv("S3_BUCKET_IMAGES")
	if err != nil {
		return storageConfig{}, err
	}

	return storageConfig{
		Endpoint:     endpoint,
		AccessKey:    accessKey,
		SecretKey:    secretKey,
		Region:       region,
		FilesBucket:  filesBucket,
		ImagesBucket: imagesBucket,
	}, nil
}

func requireStorageEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func registerMediaFileBatchRoutes(mux *http.ServeMux, imagesHandler, audioHandler, filesHandler, batchesHandler http.Handler) {
	mux.Handle("/v1/images/generations", imagesHandler)
	mux.Handle("/v1/images/edits", imagesHandler)
	mux.Handle("/v1/images/variations", imagesHandler)
	mux.Handle("/v1/audio/speech", audioHandler)
	mux.Handle("/v1/audio/transcriptions", audioHandler)
	mux.Handle("/v1/audio/translations", audioHandler)
	mux.Handle("/v1/files", filesHandler)
	mux.Handle("/v1/files/", filesHandler)
	mux.Handle("/v1/uploads", filesHandler)
	mux.Handle("/v1/uploads/", filesHandler)
	mux.Handle("/v1/batches", batchesHandler)
	mux.Handle("/v1/batches/", batchesHandler)
}

func handleModels(client *catalog.Client, authorizer *authz.Authorizer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Valid API key required to list models, even if not binding to a specific alias.
		if _, ok := authorizeAliasRequest(w, r, authorizer, "", 0, 0, 0); !ok {
			return
		}

		snapshot, err := client.FetchSnapshot(r.Context())
		if err != nil {
			writeCatalogUnavailable(w)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"object": "list",
			"data":   snapshot.Models,
		})
	})
}

func handleCatalogModels(client *catalog.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := client.FetchSnapshot(r.Context())
		if err != nil {
			writeCatalogUnavailable(w)
			return
		}

		writeJSON(w, http.StatusOK, snapshot.Catalog)
	})
}

func writeCatalogUnavailable(w http.ResponseWriter) {
	code := "catalog_unavailable"
	apierrors.WriteError(w, http.StatusServiceUnavailable, "api_error", "The Hive model catalog is temporarily unavailable.", &code)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func resolveMatrixPath() string {
	matrixPath := os.Getenv("SUPPORT_MATRIX_PATH")
	if matrixPath != "" {
		return matrixPath
	}

	return "/app/packages/openai-contract/matrix/support-matrix.json"
}

func resolveSpecPath() string {
	specPath := os.Getenv("OPENAPI_SPEC_PATH")
	if specPath != "" {
		return specPath
	}

	return "/app/packages/openai-contract/generated/hive-openapi.yaml"
}

func resolveControlPlaneBaseURL() string {
	baseURL := os.Getenv("EDGE_CONTROL_PLANE_BASE_URL")
	if baseURL != "" {
		return baseURL
	}

	return "http://control-plane:8081"
}

func resolveRedisURL() string {
	url := os.Getenv("REDIS_URL")
	if url != "" {
		return url
	}
	return "redis://redis:6379/0"
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

// buildBudgetGate constructs the Phase 14 BudgetGate middleware. The gate
// resolves the workspace by hashing the bearer token through the authz client,
// then enforces the hard cap from Redis (key written by the control-plane
// budgets service on every Set/DeleteBudget). Soft-cap crossings increment
// the `budget_soft_cap_crossed_total` counter without blocking the request.
//
// Cache invalidation strategy: the control-plane PUSHES the latest hard_cap
// to Redis on every upsert; the gate READS with a brief TTL so missed pushes
// heal on the next read. The MTD spend counter is INCRed inline by the
// control-plane settlement path keyed by `budget:mtd_spend:{ws}:YYYY-MM`.
func buildBudgetGate(authzClient *authz.Client) (*limits.BudgetGate, error) {
	opt, err := redis.ParseURL(resolveRedisURL())
	if err != nil {
		return nil, fmt.Errorf("budget gate: parse redis URL: %w", err)
	}
	redisClient := redis.NewClient(opt)
	cache := limits.NewRedisCacheReader(redisClient)

	resolver := func(r *http.Request) (string, bool) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			return "", false
		}
		// Resolve is best-effort here — auth failures will be re-rejected by
		// the per-route authz path. We only need the workspace identity.
		snap, rerr := authzClient.Resolve(r.Context(), authHeader)
		if rerr != nil {
			return "", false
		}
		if snap.AccountID == "" {
			return "", false
		}
		return snap.AccountID, true
	}

	return limits.New(limits.Config{
		Cache:                cache,
		WorkspaceFromRequest: resolver,
		// SoftCapResolver intentionally nil — soft-cap evaluation lives in the
		// control-plane spendalerts cron. Phase 18 may surface a thin
		// internal endpoint for inline soft-cap checks if hot-path needs it.
		SoftCapResolver: nil,
	})
}

// authorizeAliasRequest performs hot-path authorization.
// It writes the OpenAI-compatible error response itself if unauthorized.
// Returns the snapshot and a boolean indicating whether authorized.
func authorizeAliasRequest(w http.ResponseWriter, r *http.Request, authorizer *authz.Authorizer, aliasID string, estimatedCredits, billableTokens, freeTokens int64) (authz.AuthSnapshot, bool) {
	authHeader := r.Header.Get("Authorization")
	snapshot, headers, authErr := authorizer.Authorize(r.Context(), authHeader, aliasID, estimatedCredits, billableTokens, freeTokens)
	if authErr != nil {
		status := http.StatusUnauthorized
		if authErr.Error.Type == "insufficient_quota" {
			status = http.StatusTooManyRequests
		} else if authErr.Error.Code != nil && *authErr.Error.Code == "model_not_found" {
			status = http.StatusNotFound
		}
		if authErr.Error.Code != nil && *authErr.Error.Code == "rate_limit_exceeded" {
			apierrors.WriteRateLimitError(w, authErr.Error.Message, authErr.Error.Code, headers)
			return snapshot, false
		}
		apierrors.WriteError(w, status, authErr.Error.Type, authErr.Error.Message, authErr.Error.Code)
		return snapshot, false
	}
	return snapshot, true
}
