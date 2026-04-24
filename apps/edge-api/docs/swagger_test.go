package docs

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSwaggerHandlerServesSpecAndHTML(t *testing.T) {
	t.Helper()

	specDir := t.TempDir()
	specPath := filepath.Join(specDir, "hive-openapi.yaml")
	specBody := strings.Join([]string{
		"openapi: 3.0.0",
		"servers:",
		"  - url: /v1",
		"paths:",
		"  /models:",
		"    get:",
		"      x-hive-status: supported_now",
	}, "\n")

	if err := os.WriteFile(specPath, []byte(specBody), 0o644); err != nil {
		t.Fatalf("write fixture spec: %v", err)
	}

	handler := SwaggerHandler(specPath)

	t.Run("openapi yaml", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/docs/openapi.yaml", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/yaml" {
			t.Fatalf("content-type = %q, want application/yaml", got)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "url: /v1") {
			t.Fatalf("body missing generated server URL: %q", body)
		}
		if !strings.Contains(body, "x-hive-status: supported_now") {
			t.Fatalf("body missing Hive status annotation: %q", body)
		}
		if strings.Contains(body, "https://api.openai.com/v1") {
			t.Fatalf("body still contains upstream OpenAI URL: %q", body)
		}
	})

	t.Run("swagger ui", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/docs/", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "swagger-ui") {
			t.Fatalf("body missing swagger-ui marker: %q", body)
		}
		if !strings.Contains(body, "./openapi.yaml") {
			t.Fatalf("body missing OpenAPI URL: %q", body)
		}
	})
}

func TestSwaggerHandlerReturnsNotFoundWhenSpecIsMissing(t *testing.T) {
	handler := SwaggerHandler(filepath.Join(t.TempDir(), "missing.yaml"))

	req := httptest.NewRequest(http.MethodGet, "/docs/openapi.yaml", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if !strings.Contains(rec.Body.String(), "spec file not found") {
		t.Fatalf("missing error message in body: %q", rec.Body.String())
	}
}
