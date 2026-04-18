package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hivegpt/hive/apps/edge-api/internal/authz"
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
	authorizer := newTestAuthorizer(t, http.StatusOK, `{
		"key_id":"key-1",
		"account_id":"acc-1",
		"status":"active",
		"allow_all_models":true,
		"allowed_aliases":["hive-default","hive-fast","hive-auto"],
		"budget_kind":"none",
		"budget_consumed_credits":0,
		"budget_reserved_credits":0,
		"policy_version":1
	}`)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer hk_test")
	rr := httptest.NewRecorder()

	handleModels(client, authorizer).ServeHTTP(rr, req)

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
	authorizer := newTestAuthorizer(t, http.StatusOK, `{
		"key_id":"key-1",
		"account_id":"acc-1",
		"status":"active",
		"allow_all_models":true,
		"allowed_aliases":["hive-default"],
		"budget_kind":"none",
		"budget_consumed_credits":0,
		"budget_reserved_credits":0,
		"policy_version":1
	}`)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer hk_test")
	rr := httptest.NewRecorder()

	handleModels(client, authorizer).ServeHTTP(rr, req)

	if strings.Contains(strings.ToLower(rr.Body.String()), "openrouter") || strings.Contains(strings.ToLower(rr.Body.String()), "groq") {
		t.Fatalf("expected provider-blind response, got %s", rr.Body.String())
	}
}

func TestModelsRouteRequiresValidAPIKey(t *testing.T) {
	client := edgecatalog.NewClient(newCatalogSnapshotServer(t, `{"models":[],"catalog":[]}`))
	authorizer := newTestAuthorizer(t, http.StatusNotFound, `{"error":"not found"}`)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer hk_invalid")
	rr := httptest.NewRecorder()

	handleModels(client, authorizer).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_api_key") {
		t.Fatalf("expected invalid_api_key error, got %s", rr.Body.String())
	}
}

func TestModelsRouteUsesLimiter(t *testing.T) {
	client := edgecatalog.NewClient(newCatalogSnapshotServer(t, `{"models":[],"catalog":[]}`))
	var sawInputs struct {
		estimatedCredits int64
		billableTokens   int64
		freeTokens       int64
	}
	authorizer := newTestAuthorizerWithLimiter(t, http.StatusOK, `{
		"key_id":"key-1",
		"account_id":"acc-1",
		"status":"active",
		"allow_all_models":true,
		"allowed_aliases":["hive-default","hive-fast","hive-auto"],
		"budget_kind":"none",
		"budget_consumed_credits":0,
		"budget_reserved_credits":0,
		"account_rate_policy":{"rate_limit_rpm":120,"rate_limit_tpm":240000,"rolling_five_hour_limit":0,"weekly_limit":0,"free_token_weight_tenths":1},
		"key_rate_policy":{"rate_limit_rpm":12,"rate_limit_tpm":24000,"rolling_five_hour_limit":0,"weekly_limit":0,"free_token_weight_tenths":1},
		"policy_version":1
	}`, func(_ context.Context, snapshot authz.AuthSnapshot, aliasID string, estimatedCredits, billableTokens, freeTokens int64) (authz.LimitResult, error) {
		sawInputs.estimatedCredits = estimatedCredits
		sawInputs.billableTokens = billableTokens
		sawInputs.freeTokens = freeTokens
		return authz.LimitResult{
			Allowed:             false,
			Reason:              "request_limit_exceeded",
			RequestLimit:        12,
			RequestRemaining:    0,
			RequestResetSeconds: 21,
		}, nil
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer hk_rate_limited")
	rr := httptest.NewRecorder()

	handleModels(client, authorizer).ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("retry-after") != "21" {
		t.Fatalf("expected retry-after header, got %#v", rr.Header())
	}
	if sawInputs != (struct {
		estimatedCredits int64
		billableTokens   int64
		freeTokens       int64
	}{0, 0, 0}) {
		t.Fatalf("expected /v1/models to call limiter with zero-cost inputs, got %+v", sawInputs)
	}
}

func TestLoadStorageConfigRequiresAllS3EnvVars(t *testing.T) {
	required := []string{
		"S3_ENDPOINT",
		"S3_ACCESS_KEY",
		"S3_SECRET_KEY",
		"S3_REGION",
		"S3_BUCKET_FILES",
		"S3_BUCKET_IMAGES",
	}

	for _, missing := range required {
		t.Run(missing, func(t *testing.T) {
			setValidStorageEnv(t)
			t.Setenv(missing, "")

			_, err := loadStorageConfigFromEnv()
			if err == nil || !strings.Contains(err.Error(), missing+" is required") {
				t.Fatalf("loadStorageConfigFromEnv() error = %v, want containing %q", err, missing+" is required")
			}
		})
	}
}

func TestLoadStorageConfigAcceptsSupabasePathEndpoint(t *testing.T) {
	setValidStorageEnv(t)

	cfg, err := loadStorageConfigFromEnv()
	if err != nil {
		t.Fatalf("loadStorageConfigFromEnv() returned error: %v", err)
	}
	if cfg.Endpoint != "https://project.supabase.co/storage/v1/s3" {
		t.Fatalf("Endpoint = %q, want Supabase S3 path endpoint", cfg.Endpoint)
	}
	if cfg.Region != "us-east-1" {
		t.Fatalf("Region = %q, want us-east-1", cfg.Region)
	}
	if cfg.FilesBucket != "hive-files" {
		t.Fatalf("FilesBucket = %q, want hive-files", cfg.FilesBucket)
	}
	if cfg.ImagesBucket != "hive-images" {
		t.Fatalf("ImagesBucket = %q, want hive-images", cfg.ImagesBucket)
	}
}

func TestRegisterMediaFileBatchRoutesRegistersAllPublicPaths(t *testing.T) {
	mux := http.NewServeMux()
	registerMediaFileBatchRoutes(
		mux,
		testRouteHandler("images"),
		testRouteHandler("audio"),
		testRouteHandler("files"),
		testRouteHandler("batches"),
	)

	tests := []struct {
		path        string
		wantHeader  string
		wantPattern string
	}{
		{path: "/v1/images/generations", wantHeader: "images", wantPattern: "/v1/images/generations"},
		{path: "/v1/audio/speech", wantHeader: "audio", wantPattern: "/v1/audio/speech"},
		{path: "/v1/files", wantHeader: "files", wantPattern: "/v1/files"},
		{path: "/v1/uploads", wantHeader: "files", wantPattern: "/v1/uploads"},
		{path: "/v1/batches", wantHeader: "batches", wantPattern: "/v1/batches"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			handler, pattern := mux.Handler(req)
			if pattern != tt.wantPattern {
				t.Fatalf("ServeMux pattern for %s = %q, want %q", tt.path, pattern, tt.wantPattern)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusNoContent {
				t.Fatalf("%s returned status %d, want 204", tt.path, rr.Code)
			}
			if got := rr.Header().Get("X-Test-Handler"); got != tt.wantHeader {
				t.Fatalf("%s matched handler %q, want %q", tt.path, got, tt.wantHeader)
			}
		})
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

func newTestAuthorizer(t *testing.T, status int, body string) *authz.Authorizer {
	return newTestAuthorizerWithLimiter(t, status, body, func(_ context.Context, snapshot authz.AuthSnapshot, aliasID string, estimatedCredits, billableTokens, freeTokens int64) (authz.LimitResult, error) {
		return authz.LimitResult{Allowed: true}, nil
	})
}

func newTestAuthorizerWithLimiter(t *testing.T, status int, body string, check func(context.Context, authz.AuthSnapshot, string, int64, int64, int64) (authz.LimitResult, error)) *authz.Authorizer {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/apikeys/resolve" {
			t.Fatalf("expected auth resolve path, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	client, err := authz.NewClient(server.URL, "redis://127.0.0.1:6379/0")
	if err != nil {
		t.Fatalf("new authz client: %v", err)
	}

	limiter := &authz.Limiter{
		CheckOverride: check,
	}

	return authz.NewAuthorizer(client, limiter)
}

func setValidStorageEnv(t *testing.T) {
	t.Helper()
	t.Setenv("S3_ENDPOINT", "https://project.supabase.co/storage/v1/s3")
	t.Setenv("S3_ACCESS_KEY", "test-access")
	t.Setenv("S3_SECRET_KEY", "test-secret")
	t.Setenv("S3_REGION", "us-east-1")
	t.Setenv("S3_BUCKET_FILES", "hive-files")
	t.Setenv("S3_BUCKET_IMAGES", "hive-images")
}

func testRouteHandler(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test-Handler", name)
		w.WriteHeader(http.StatusNoContent)
	})
}
