package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hivegpt/hive/apps/edge-api/docs"
	"github.com/hivegpt/hive/apps/edge-api/internal/audio"
	"github.com/hivegpt/hive/apps/edge-api/internal/auth"
	"github.com/hivegpt/hive/apps/edge-api/internal/authz"
	"github.com/hivegpt/hive/apps/edge-api/internal/batches"
	"github.com/hivegpt/hive/apps/edge-api/internal/catalog"
	"github.com/hivegpt/hive/apps/edge-api/internal/chat"
	apierrors "github.com/hivegpt/hive/apps/edge-api/internal/errors"
	"github.com/hivegpt/hive/apps/edge-api/internal/files"
	"github.com/hivegpt/hive/apps/edge-api/internal/images"
	"github.com/hivegpt/hive/apps/edge-api/internal/inference"
	"github.com/hivegpt/hive/apps/edge-api/internal/limits"
	"github.com/hivegpt/hive/apps/edge-api/internal/matrix"
	"github.com/hivegpt/hive/apps/edge-api/internal/middleware"
	"github.com/hivegpt/hive/apps/edge-api/internal/proxy"
	"github.com/hivegpt/hive/packages/storage"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// jwtAuthEnv collects the Supabase JWT validator configuration sourced
// from the runtime environment. All three values are required so the
// edge-api fails fast when JWT routing is mis-deployed.
type jwtAuthEnv struct {
	Issuer   string
	Audience string
	JWKSURL  string
}

type storageConfig struct {
	Endpoint     string
	AccessKey    string
	SecretKey    string
	Region       string
	FilesBucket  string
	ImagesBucket string
}

func main() {
	// Root context cancels on SIGINT/SIGTERM so background goroutines
	// rooted here (notably the jwx JWKS auto-refresher) exit cleanly
	// instead of leaking through process shutdown — passing
	// context.Background() to NewSupabaseJWTValidator would orphan
	// the refresh loop.
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
	dbPool := openOptionalDBPool(rootCtx)
	if dbPool != nil {
		defer dbPool.Close()
	}

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
	chatDispatchHandler := chat.NewDispatch(chat.Deps{
		Pool:       dbPool,
		LiteLLMURL: resolveLiteLLMBaseURL(),
		LiteLLMKey: resolveLiteLLMMasterKey(),
		DeploySHA:  os.Getenv("DEPLOY_SHA"),
		Env:        os.Getenv("HIVE_ENV"),
	})

	mux.Handle("/v1/chat/completions", jwtAwareChatHandler(chatDispatchHandler, inferenceHandler))
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

	// Phase 19 — Supabase JWT validator + Authorization selector.
	//
	// The selector inspects the Authorization header: requests bearing the
	// canonical "Bearer hk_" API-key prefix flow to the existing API-key
	// path (unchanged); everything else is routed through the Supabase JWT
	// middleware which validates the token, populates the request context
	// via auth.WithUser, and emits OpenAI-shaped UNAUTHORIZED errors on
	// failure. The API-key handler remains responsible for its own
	// per-route authz (`handleModels`, `authorizeAliasRequest`, etc.).
	// Phase 19 JWT validation is opt-in: when the Supabase env vars are
	// absent (CI smoke runs, single-tenant API-key-only deployments) we
	// log and skip the selector + JWT middleware wiring so non-hk_ bearer
	// tokens continue to be rejected by the existing API-key handler
	// rather than crashing the process. Production deployments are
	// expected to provide every variable; the warning here is loud enough
	// for operators to catch.
	jwtCfg, jwtCfgErr := loadJWTAuthEnv()
	var jwtMW func(http.Handler) http.Handler
	if jwtCfgErr != nil {
		log.Printf("WARNING: phase-19 JWT auth wiring skipped (%v)", jwtCfgErr)
	} else {
		jwtValidator, err := auth.NewSupabaseJWTValidator(rootCtx, auth.SupabaseJWTConfig{
			Issuer:      jwtCfg.Issuer,
			JWKSURL:     jwtCfg.JWKSURL,
			JWTAudience: jwtCfg.Audience,
		})
		if err != nil {
			log.Fatalf("failed to initialize Supabase JWT validator: %v", err)
		}
		jwtMW = auth.JWTMiddleware(jwtValidator, jwtAuditLogger())
	}

	var handler http.Handler = mux
	handler = middleware.UnsupportedEndpointMiddleware(m)(handler)
	// TODO(phase-19-plan-03): budgetGate still resolves the workspace
	// identity from the API-key bearer token via authzClient.Resolve.
	// Non-hk_ Bearer JWTs do not map there today, so quota enforcement is
	// inert for JWT-authenticated traffic — the JWT path remains
	// pre-billing in Plan 02 by design. Plan 03 will introduce a
	// ctx-aware budget resolver that reads auth.UserFrom before falling
	// back to the API-key path.
	handler = budgetGate.Wrap(handler)
	if jwtMW != nil {
		// Auth selector sits inside metrics/CompatHeaders so 401s are still
		// observed and CORS headers still apply, but outside budget/route
		// middleware so unauthenticated traffic never reaches accounting.
		handler = authSelectorMiddleware(jwtMW, handler)
	}
	handler = proxy.InstrumentHandler(edgeMetrics, handler)
	handler = middleware.CompatHeaders()(handler)

	log.Printf("edge-api listening on :%s", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func openOptionalDBPool(ctx context.Context) *pgxpool.Pool {
	dsn := strings.TrimSpace(os.Getenv("SUPABASE_DB_URL"))
	if dsn == "" {
		log.Printf("WARNING: edge-api DB pool unavailable (SUPABASE_DB_URL missing); JWT chat trace/audit writes disabled")
		return nil
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Printf("WARNING: edge-api DB pool unavailable: %v", err)
		return nil
	}
	return pool
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

func jwtAwareChatHandler(jwtHandler, apiKeyHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, ok := auth.UserFrom(r.Context()); ok && user != nil {
			jwtHandler.ServeHTTP(w, r)
			return
		}
		apiKeyHandler.ServeHTTP(w, r)
	})
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

// loadJWTAuthEnv reads the Supabase JWT validator configuration from the
// environment. Returns a non-nil error when any required variable is
// missing; callers decide whether to fatal or skip the JWT path. Phase
// 19 deployments that serve chat-app traffic MUST set every variable —
// the caller's warning + skip is intended only for CI smoke runs and
// single-tenant API-key-only deployments where JWT validation is moot.
func loadJWTAuthEnv() (jwtAuthEnv, error) {
	issuer := strings.TrimSpace(os.Getenv("SUPABASE_JWT_ISSUER"))
	audience := strings.TrimSpace(os.Getenv("SUPABASE_JWT_AUDIENCE"))
	jwksURL := strings.TrimSpace(os.Getenv("SUPABASE_JWKS_URL"))

	var missing []string
	if issuer == "" {
		missing = append(missing, "SUPABASE_JWT_ISSUER")
	}
	if audience == "" {
		missing = append(missing, "SUPABASE_JWT_AUDIENCE")
	}
	if jwksURL == "" {
		missing = append(missing, "SUPABASE_JWKS_URL")
	}
	if len(missing) > 0 {
		return jwtAuthEnv{}, fmt.Errorf("Supabase JWT config missing required env vars: %s", strings.Join(missing, ", "))
	}

	// Enforce HTTPS for JWKS. An http:// URL would let an on-path
	// attacker substitute the JWKS document and forge arbitrary JWTs
	// that the validator would accept as legitimate Supabase tokens.
	if !strings.HasPrefix(strings.ToLower(jwksURL), "https://") {
		return jwtAuthEnv{}, fmt.Errorf("SUPABASE_JWKS_URL must be https (got %q)", jwksURL)
	}

	return jwtAuthEnv{Issuer: issuer, Audience: audience, JWKSURL: jwksURL}, nil
}

// jwtAuditLogger returns the audit hook handed to the JWT middleware. For
// now this is a thin log.Printf shim — the dedicated edge-api audit.Logger
// is wired in a follow-up so we do not introduce that import here. The
// shape (`action, reason, ip`) matches the canonical control-plane audit
// signature so swapping in the real logger is mechanical.
func jwtAuditLogger() auth.AuditFailFunc {
	return func(action, reason, ip string) {
		log.Printf("auth.jwt.failure action=%s ip=%s reason=%s", action, ip, reason)
	}
}

// authSelectorMiddleware routes only Hive-versioned `/v1/*` traffic through
// the auth Selector. Infrastructure endpoints (/health, /metrics, /docs/,
// /catalog/models) bypass authentication so probes and the Swagger UI keep
// working. Within /v1, the Selector forwards "Bearer hk_" credentials to
// the existing API-key path (the inner handler / authorizer pair) and
// everything else through the JWT middleware.
//
// TODO(phase-19-plan-03): downstream handlers (handleModels,
// inferenceHandler, images/audio/files/batches) still authorize via the
// API-key authorizer.Authorize path. A successful JWT request passes
// through the middleware with auth.WithUser populated, then 401s at the
// handler because no API-key snapshot resolves. Plan 03 introduces a
// shared authorizer-from-ctx adapter (`authz.FromUserContext`) so JWT
// principals reach the existing handlers without a parallel route tree.
// Plan 02 ships the JWT validator + selector wiring only.
func authSelectorMiddleware(jwtMW func(http.Handler) http.Handler, next http.Handler) http.Handler {
	jwtPath := jwtMW(next)
	selector := auth.Selector(jwtPath, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		selector.ServeHTTP(w, r)
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
