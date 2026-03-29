package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/hivegpt/hive/apps/edge-api/docs"
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

	// Create the main mux
	mux := http.NewServeMux()

	// Infrastructure routes (no unsupported middleware)
	mux.HandleFunc("/health", handleHealth)

	// Swagger docs (no unsupported middleware)
	swaggerHandler := docs.SwaggerHandler(specPath)
	mux.Handle("/docs/", swaggerHandler)

	// API routes
	mux.HandleFunc("/v1/models", handleModels)

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

func handleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   []interface{}{},
	})
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
