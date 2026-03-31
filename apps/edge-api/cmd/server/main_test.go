package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	edgecatalog "github.com/hivegpt/hive/apps/edge-api/internal/catalog"
)

func TestResolveSpecPathDefaultsToGeneratedHiveContract(t *testing.T) {
	t.Setenv("OPENAPI_SPEC_PATH", "")

	got := resolveSpecPath()

	want := "/app/packages/openai-contract/generated/hive-openapi.yaml"
	if got != want {
		t.Fatalf("resolveSpecPath() = %q, want %q", got, want)
	}
}

func TestResolveSpecPathHonorsOverride(t *testing.T) {
	t.Setenv("OPENAPI_SPEC_PATH", "/tmp/override.yaml")

	got := resolveSpecPath()

	if got != "/tmp/override.yaml" {
		t.Fatalf("resolveSpecPath() = %q, want override path", got)
	}
}

func TestHandleModelsReturnsSeededHiveAliases(t *testing.T) {
	client := edgecatalog.NewClient(newCatalogSnapshotServer(t, `{
		"models": [
			{"id":"hive-default","object":"model","created":1716935002,"owned_by":"hive"},
			{"id":"hive-fast","object":"model","created":1716935003,"owned_by":"hive"},
			{"id":"hive-auto","object":"model","created":1716935004,"owned_by":"hive"}
		],
		"catalog": []
	}`))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()

	handleModels(client).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	for _, alias := range []string{"hive-default", "hive-fast", "hive-auto"} {
		if !strings.Contains(rr.Body.String(), alias) {
			t.Fatalf("expected response to contain %q, got %s", alias, rr.Body.String())
		}
	}
}

func TestHandleCatalogModelsReturnsPricingMetadata(t *testing.T) {
	client := edgecatalog.NewClient(newCatalogSnapshotServer(t, `{
		"models": [],
		"catalog": [
			{
				"id":"hive-default",
				"display_name":"Hive Default",
				"summary":"Balanced default chat model.",
				"capability_badges":["stable","chat","responses"],
				"pricing":{"input_price_credits":12,"output_price_credits":36,"cache_read_price_credits":2,"cache_write_price_credits":6},
				"lifecycle":"stable"
			}
		]
	}`))

	req := httptest.NewRequest(http.MethodGet, "/catalog/models", nil)
	rr := httptest.NewRecorder()

	handleCatalogModels(client).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	for _, field := range []string{"input_price_credits", "output_price_credits", "cache_read_price_credits"} {
		if !strings.Contains(rr.Body.String(), field) {
			t.Fatalf("expected response to contain %q, got %s", field, rr.Body.String())
		}
	}
}

func TestHandleModelsDoesNotLeakProviderNames(t *testing.T) {
	client := edgecatalog.NewClient(newCatalogSnapshotServer(t, `{
		"models": [
			{"id":"hive-default","object":"model","created":1716935002,"owned_by":"hive"}
		],
		"catalog": [
			{
				"id":"hive-default",
				"display_name":"Hive Default",
				"summary":"Fallback to openrouter and groq when needed.",
				"capability_badges":["stable","chat","responses"],
				"pricing":{"input_price_credits":12,"output_price_credits":36},
				"lifecycle":"stable"
			}
		]
	}`))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()

	handleModels(client).ServeHTTP(rr, req)

	if strings.Contains(strings.ToLower(rr.Body.String()), "openrouter") || strings.Contains(strings.ToLower(rr.Body.String()), "groq") {
		t.Fatalf("expected provider-blind response, got %s", rr.Body.String())
	}
}

func newCatalogSnapshotServer(t *testing.T, body string) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/catalog/snapshot" {
			t.Fatalf("expected snapshot path, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	return server.URL
}
