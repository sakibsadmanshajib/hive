package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/hivegpt/hive/apps/edge-api/docs"
	apierrors "github.com/hivegpt/hive/apps/edge-api/internal/errors"
	"github.com/hivegpt/hive/apps/edge-api/internal/catalog"
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

	// Create the main mux
	mux := http.NewServeMux()

	// Infrastructure routes (no unsupported middleware)
	mux.HandleFunc("/health", handleHealth)

	// Swagger docs (no unsupported middleware)
	swaggerHandler := docs.SwaggerHandler(specPath)
	mux.Handle("/docs/", swaggerHandler)

	// API routes
	mux.Handle("/v1/models", handleModels(catalogClient))
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

func handleModels(client *catalog.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
