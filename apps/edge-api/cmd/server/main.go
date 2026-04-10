package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/hivegpt/hive/apps/edge-api/docs"
	"github.com/hivegpt/hive/apps/edge-api/internal/audio"
	"github.com/hivegpt/hive/apps/edge-api/internal/authz"
	"github.com/hivegpt/hive/apps/edge-api/internal/catalog"
	apierrors "github.com/hivegpt/hive/apps/edge-api/internal/errors"
	"github.com/hivegpt/hive/apps/edge-api/internal/files"
	"github.com/hivegpt/hive/apps/edge-api/internal/images"
	"github.com/hivegpt/hive/apps/edge-api/internal/inference"
	"github.com/hivegpt/hive/apps/edge-api/internal/matrix"
	"github.com/hivegpt/hive/apps/edge-api/internal/middleware"
)

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

	// Create the main mux
	mux := http.NewServeMux()

	// Infrastructure routes (no unsupported middleware)
	mux.HandleFunc("/health", handleHealth)

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

	// Image and audio routes
	storageClient, err := files.NewStorageClient(
		os.Getenv("S3_ENDPOINT"),
		os.Getenv("S3_ACCESS_KEY"),
		os.Getenv("S3_SECRET_KEY"),
		os.Getenv("S3_USE_SSL") == "true",
	)
	if err != nil {
		log.Fatalf("failed to initialize storage client: %v", err)
	}
	imageBucket := os.Getenv("S3_BUCKET_IMAGES")
	if imageBucket == "" {
		imageBucket = "hive-images"
	}

	imagesHandler := images.NewHandler(
		resolveLiteLLMBaseURL(),
		resolveLiteLLMMasterKey(),
		&storageAdapter{client: storageClient},
		imageBucket,
	)
	mux.Handle("/v1/images/generations", imagesHandler)
	mux.Handle("/v1/images/edits", imagesHandler)
	mux.Handle("/v1/images/variations", imagesHandler)

	audioHandler := audio.NewHandler(
		resolveLiteLLMBaseURL(),
		resolveLiteLLMMasterKey(),
	)
	mux.Handle("/v1/audio/speech", audioHandler)
	mux.Handle("/v1/audio/transcriptions", audioHandler)
	mux.Handle("/v1/audio/translations", audioHandler)

	// Files and Uploads API routes
	filesBucket := os.Getenv("S3_BUCKET_FILES")
	if filesBucket == "" {
		filesBucket = "hive-files"
	}
	filestoreClient := files.NewFilestoreClient(resolveControlPlaneBaseURL())
	filesAuthorizer := files.NewAuthorizerAdapter(authorizer)
	filesHandler := files.NewHandler(filesAuthorizer, storageClient, filestoreClient, filesBucket)
	mux.Handle("/v1/files", filesHandler)
	mux.Handle("/v1/files/", filesHandler)
	mux.Handle("/v1/uploads", filesHandler)
	mux.Handle("/v1/uploads/", filesHandler)

	// API routes
	mux.Handle("/v1/models", handleModels(catalogClient, authorizer))
	mux.Handle("/catalog/models", handleCatalogModels(catalogClient))

	// Apply middleware: CompatHeaders (outermost) -> UnsupportedEndpoint (inner)
	var handler http.Handler = mux
	handler = middleware.UnsupportedEndpointMiddleware(m)(handler)
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

// storageAdapter adapts *files.StorageClient (returns *url.URL) to images.StorageInterface (returns string).
type storageAdapter struct {
	client *files.StorageClient
}

func (a *storageAdapter) Upload(ctx context.Context, bucket, key string, reader io.Reader, size int64, contentType string) error {
	return a.client.Upload(ctx, bucket, key, reader, size, contentType)
}

func (a *storageAdapter) PresignedURL(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	u, err := a.client.PresignedURL(ctx, bucket, key, ttl)
	if err != nil {
		return "", err
	}
	return u.String(), nil
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
