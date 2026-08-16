package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
	edgecatalog "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/catalog"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/files"
	"github.com/sakibsadmanshajib/hive/packages/storage"
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
	var sawPath string
	seeded := `{
		"models": [
			{"id":"hive-default","object":"model","created":1716935002,"owned_by":"hive"},
			{"id":"hive-fast","object":"model","created":1716935003,"owned_by":"hive"},
			{"id":"hive-auto","object":"model","created":1716935004,"owned_by":"hive"}
		],
		"catalog": []
	}`
	client := edgecatalog.NewClient(newTenantCatalogSnapshotServer(t, seeded, seeded, &sawPath))
	authorizer := newTestAuthorizer(t, http.StatusOK, `{
		"key_id":"key-1",
		"account_id":"acc-1",
		"tenant_id":"`+uuid.New().String()+`",
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
	var sawPath string
	seeded := `{
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
	}`
	client := edgecatalog.NewClient(newTenantCatalogSnapshotServer(t, seeded, seeded, &sawPath))
	authorizer := newTestAuthorizer(t, http.StatusOK, `{
		"key_id":"key-1",
		"account_id":"acc-1",
		"tenant_id":"`+uuid.New().String()+`",
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

// testOWUIShimKey mirrors the shape a deployment is supposed to configure:
// OWUI_SHIM_KEY is documented as a minted Hive API key, so it carries the hk_
// prefix. It deliberately resolves to nothing here, which is the state the demo
// box was actually in when Open WebUI's model picker came up empty.
const testOWUIShimKey = "hk_owui_shim_test"

// TestModelsRouteRejectsUnresolvableOWUIShimKey is the regression guard for the
// symptom fix this change replaced. An earlier revision admitted the OWUI shim
// key on GET /v1/models with no key lookup, so the model picker populated while
// the very same credential still failed on Open WebUI's document-RAG embeddings
// and text-to-speech, and nothing anywhere named the cause. Model listing must
// keep requiring a credential this service can resolve, so a dead shim key
// cannot look healthy.
func TestModelsRouteRejectsUnresolvableOWUIShimKey(t *testing.T) {
	client := edgecatalog.NewClient(newCatalogSnapshotServer(t, `{
		"models": [
			{"id":"hive-default","object":"model","created":1716935002,"owned_by":"hive"}
		],
		"catalog": []
	}`))
	authorizer := newTestAuthorizer(t, http.StatusNotFound, `{"error":"not found"}`)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+testOWUIShimKey)
	rr := httptest.NewRecorder()

	handleModels(client, authorizer).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for an unresolvable shim key, got %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "hive-default") {
		t.Fatalf("catalog must not be served to an unresolvable credential, got %s", rr.Body.String())
	}
}

// TestOWUIShimKeyIsNeverSpecialCasedOnAuthorizedRoutes pins containment: no
// authorized route branches on "is this the OWUI shim key". Every one of them
// resolves its credential through authorizeAliasRequest, which knows nothing
// about that value.
func TestOWUIShimKeyIsNeverSpecialCasedOnAuthorizedRoutes(t *testing.T) {
	paths := []string{
		"/v1/models",
		"/v1/chat/completions",
		"/v1/embeddings",
		"/v1/audio/speech",
		"/v1/responses",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			authorizer := newTestAuthorizer(t, http.StatusNotFound, `{"error":"not found"}`)
			req := httptest.NewRequest(http.MethodPost, path, nil)
			req.Header.Set("Authorization", "Bearer "+testOWUIShimKey)
			rr := httptest.NewRecorder()

			if _, ok := authorizeAliasRequest(rr, req, authorizer, "hive-default", 0, 0, 0); ok {
				t.Fatalf("shim key must not authorize %s", path)
			}
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401 on %s, got %d: %s", path, rr.Code, rr.Body.String())
			}
		})
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

func TestSharedStorageClientSatisfiesFilesStorageBackend(t *testing.T) {
	var _ files.StorageBackend = (*storage.S3Client)(nil)
	var sharedParts []storage.CompletePart
	var fileParts []files.CompletePart = sharedParts
	if len(fileParts) != 0 {
		t.Fatalf("expected empty part slice")
	}
}

// identityMiddleware is a no-op http.Handler wrapper. Route-registration
// tests use it in place of the real Voice featuregate.Require middleware so
// they exercise mux wiring only; gate enforcement itself is covered by
// apps/edge-api/internal/featuregate's own tests.
func identityMiddleware(h http.Handler) http.Handler { return h }

// The public mux is served on the port published through the ingress tunnel.
// hive_upstream_requests_total labels every series with the upstream provider
// name, which the provider-blind rule forbids exposing to customers, so
// /metrics belongs on the telemetry listener (metricsListenAddr) and must not
// be registered alongside the other unauthenticated infrastructure routes.
func TestRegisterInfraRoutesDoesNotExposeMetrics(t *testing.T) {
	mux := http.NewServeMux()
	registerInfraRoutes(mux, "testdata/openapi.yaml")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /metrics on the public mux = %d, want %d", rec.Code, http.StatusNotFound)
	}

	// Guards the assertion above against passing vacuously.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health = %d, want %d", rec.Code, http.StatusOK)
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
		identityMiddleware,
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

// TestRegisterMediaFileBatchRoutesAppliesVoiceGateToAudioOnly is the
// acceptance-check test for issue #293's Voice route-enforcement gap: the
// voiceMW middleware passed to registerMediaFileBatchRoutes must wrap every
// /v1/audio/* route and no other route.
func TestRegisterMediaFileBatchRoutesAppliesVoiceGateToAudioOnly(t *testing.T) {
	tagging := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Voice-Gate", "applied")
			h.ServeHTTP(w, r)
		})
	}

	mux := http.NewServeMux()
	registerMediaFileBatchRoutes(
		mux,
		testRouteHandler("images"),
		testRouteHandler("audio"),
		testRouteHandler("files"),
		testRouteHandler("batches"),
		tagging,
	)

	tests := []struct {
		path string
		want string
	}{
		{path: "/v1/audio/speech", want: "applied"},
		{path: "/v1/audio/transcriptions", want: "applied"},
		{path: "/v1/audio/translations", want: "applied"},
		{path: "/v1/images/generations", want: ""},
		{path: "/v1/files", want: ""},
		{path: "/v1/batches", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			handler, _ := mux.Handler(req)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if got := rec.Header().Get("X-Voice-Gate"); got != tt.want {
				t.Errorf("%s: X-Voice-Gate = %q, want %q", tt.path, got, tt.want)
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

// newTenantCatalogSnapshotServer answers both snapshot shapes: the global
// snapshot and the per-tenant one. It records which path was requested so a
// test can assert the tenant-scoped list is the one served.
func newTenantCatalogSnapshotServer(t *testing.T, globalBody, tenantBody string, sawPath *string) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/internal/catalog/snapshot/tenant/") {
			_, _ = w.Write([]byte(tenantBody))
			return
		}
		_, _ = w.Write([]byte(globalBody))
	}))
	t.Cleanup(server.Close)

	return server.URL
}

// TestHandleModelsFiltersByTenantForJWTSession asserts /v1/models serves the
// tenant-filtered list for a JWT session, so the list a tenant sees matches
// what the tenant may actually invoke.
func TestHandleModelsFiltersByTenantForJWTSession(t *testing.T) {
	var sawPath string
	client := edgecatalog.NewClient(newTenantCatalogSnapshotServer(t,
		`{"models":[{"id":"hive-default","object":"model","created":1,"owned_by":"hive"},{"id":"hive-blocked","object":"model","created":2,"owned_by":"hive"}],"catalog":[]}`,
		`{"models":[{"id":"hive-default","object":"model","created":1,"owned_by":"hive"}],"catalog":[]}`,
		&sawPath,
	))
	// No API key on this request: the JWT middleware already authenticated it.
	authorizer := newTestAuthorizer(t, http.StatusNotFound, `{}`)

	tenantID := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: uuid.New(), TenantID: tenantID, Role: "member"}))
	rr := httptest.NewRecorder()

	handleModels(client, authorizer).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for a JWT session, got %d: %s", rr.Code, rr.Body.String())
	}
	if want := "/internal/catalog/snapshot/tenant/" + tenantID.String(); sawPath != want {
		t.Fatalf("expected tenant snapshot path %q, got %q", want, sawPath)
	}
	if strings.Contains(rr.Body.String(), "hive-blocked") {
		t.Fatalf("tenant-filtered list must not include a hidden alias: %s", rr.Body.String())
	}
}

// TestHandleModelsFiltersByTenantForAPIKeyCaller is the D-030 regression
// guard: an API key is now tenant-scoped (control-plane resolves its
// account's tenant via public.tenant_billing_accounts, see
// apikeys.Service.ResolveSnapshot), so /v1/models must serve the
// tenant-filtered list for it the same way it already does for a JWT
// session -- not the unfiltered global catalog every API key saw before
// this. This replaces the old TestHandleModelsKeepsGlobalListForAPIKeyCaller,
// which pinned exactly the gap this PR closes.
func TestHandleModelsFiltersByTenantForAPIKeyCaller(t *testing.T) {
	var sawPath string
	client := edgecatalog.NewClient(newTenantCatalogSnapshotServer(t,
		`{"models":[{"id":"hive-default","object":"model","created":1,"owned_by":"hive"},{"id":"hive-blocked","object":"model","created":2,"owned_by":"hive"}],"catalog":[]}`,
		`{"models":[{"id":"hive-default","object":"model","created":1,"owned_by":"hive"}],"catalog":[]}`,
		&sawPath,
	))
	tenantID := uuid.New()
	authorizer := newTestAuthorizer(t, http.StatusOK, `{
		"key_id":"key-1",
		"account_id":"acc-1",
		"tenant_id":"`+tenantID.String()+`",
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

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if want := "/internal/catalog/snapshot/tenant/" + tenantID.String(); sawPath != want {
		t.Fatalf("expected tenant snapshot path %q, got %q", want, sawPath)
	}
	if strings.Contains(rr.Body.String(), "hive-blocked") {
		t.Fatalf("tenant-filtered list must not include a hidden alias: %s", rr.Body.String())
	}
}

// TestHandleModelsRefusesAPIKeyWithoutTenant is the fail-closed guard: an API
// key whose account has no public.tenant_billing_accounts row (tenant_id
// empty on the resolved snapshot) must be refused, not served the unfiltered
// catalog as a fallback. Without a tenant, entitlement cannot be checked at
// all, so admitting the request would silently reopen the gap D-030 exists
// to close.
func TestHandleModelsRefusesAPIKeyWithoutTenant(t *testing.T) {
	client := edgecatalog.NewClient(newCatalogSnapshotServer(t, `{
		"models": [{"id":"hive-default","object":"model","created":1,"owned_by":"hive"}],
		"catalog": []
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

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for an API key with no resolvable tenant, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "account_not_provisioned") {
		t.Fatalf("expected account_not_provisioned code, got %s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "hive-default") {
		t.Fatalf("an unprovisioned API key must not see the catalog: %s", rr.Body.String())
	}
}

// resolveBodyForTenant is the control-plane resolve payload a minted, active,
// allow-all-models API key produces, parameterised only by the tenant its
// account maps to. Shared by the two issue #717 guards below so the probe and
// the request path are provably fed the same key state.
func resolveBodyForTenant(tenantID string) string {
	return `{
		"key_id":"key-1",
		"account_id":"acc-1",
		"tenant_id":"` + tenantID + `",
		"status":"active",
		"allow_all_models":true,
		"allowed_aliases":["hive-default"],
		"budget_kind":"none",
		"budget_consumed_credits":0,
		"budget_reserved_credits":0,
		"policy_version":1
	}`
}

// TestShimKeyProbeAgreesWithTheModelsRequestPath is the issue #717 drift guard.
//
// The startup probe used to assert a weaker predicate than the request path:
// it read Status and the model allowlist but never TenantID, which is the one
// field handleModels requires. So edge-api logged "OWUI_SHIM_KEY resolves to an
// active Hive API key; Open WebUI model listing, document RAG embeddings, and
// text-to-speech can authenticate" and then answered 403
// account_not_provisioned to all three, for every demo user, because the shim
// key's account had no public.tenant_billing_accounts row. The false green is
// why the outage survived: it actively asserted the opposite of the truth.
//
// This test pins the invariant rather than that one field: for the same
// resolved key state, the probe's verdict and the request path's verdict must
// agree. Add a requirement to one side without the other and this fails.
func TestShimKeyProbeAgreesWithTheModelsRequestPath(t *testing.T) {
	cases := []struct {
		name       string
		tenantID   string
		wantStatus int
	}{
		{"account with no billing mapping", "", http.StatusForbidden},
		{"tenant id that does not parse", "not-a-uuid", http.StatusForbidden},
		{"explicitly nil tenant id", uuid.Nil.String(), http.StatusForbidden},
		{"provisioned account", uuid.New().String(), http.StatusOK},
	}
	const models = `{"models":[{"id":"hive-default","object":"model","created":1,"owned_by":"hive"}],"catalog":[]}`

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sawPath string
			client := edgecatalog.NewClient(newTenantCatalogSnapshotServer(t, models, models, &sawPath))
			authorizer := newTestAuthorizer(t, http.StatusOK, resolveBodyForTenant(tc.tenantID))

			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			req.Header.Set("Authorization", "Bearer "+testOWUIShimKey)
			rr := httptest.NewRecorder()
			handleModels(client, authorizer).ServeHTTP(rr, req)
			served := rr.Code == http.StatusOK

			resolver := &stubShimKeyResolver{snapshot: authz.AuthSnapshot{
				TenantID:       tc.tenantID,
				Status:         "active",
				AllowAllModels: true,
			}}
			probeErr := checkOWUIShimKey(context.Background(), resolver, testOWUIShimKey)

			// Pin the exact refusal, not merely "not 200": a 401 or a 503 would
			// satisfy the agreement check below while meaning something else
			// entirely, and an assertion that cannot tell those apart is the
			// same shape of non-evidence as the probe this test exists for.
			if rr.Code != tc.wantStatus {
				t.Fatalf("request path status=%d, want %d: %s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if tc.wantStatus == http.StatusForbidden &&
				!strings.Contains(rr.Body.String(), "account_not_provisioned") {
				t.Fatalf("expected account_not_provisioned, got %s", rr.Body.String())
			}
			if healthy := probeErr == nil; healthy != served {
				t.Fatalf("probe reports healthy=%v but the request path served=%v; the probe must never "+
					"claim a key can authenticate that /v1/models then refuses (probe verdict: %v)",
					healthy, served, probeErr)
			}
		})
	}
}

// TestCheckOWUIShimKeyRejectsAnUnprovisionedAccount is the narrow half of the
// guard above: a key that is active and allowed every model, but whose account
// resolves no tenant, is unusable and the probe must say so in the words an
// operator can act on. The old predicate accepted exactly this state.
func TestCheckOWUIShimKeyRejectsAnUnprovisionedAccount(t *testing.T) {
	for _, tenantID := range []string{"", "   ", "not-a-uuid", uuid.Nil.String()} {
		t.Run("tenant_id="+tenantID, func(t *testing.T) {
			resolver := &stubShimKeyResolver{snapshot: authz.AuthSnapshot{
				TenantID:       tenantID,
				Status:         "active",
				AllowAllModels: true,
				AllowedAliases: []string{"hive-embedding-default"},
			}}

			err := checkOWUIShimKey(context.Background(), resolver, testOWUIShimKey)

			if err == nil {
				t.Fatalf("expected an unusable verdict for tenant_id %q, got nil", tenantID)
			}
			for _, want := range []string{"not provisioned", "tenant_billing_accounts"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("expected the verdict to name %q, got %q", want, err.Error())
				}
			}
		})
	}
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

// TestVoiceGateForAPIKeysBypassesGateForAPIKeyCallers proves the fix for the
// hk_-caller lockout: auth.Selector never populates auth.UserFrom for
// "Bearer hk_..." requests (see internal/auth/selector.go), so wrapping
// /v1/audio/* directly in featureGate.Require denied every API-key caller
// unconditionally, regardless of the tenant's ENABLE_VOICE setting or any
// routing/catalog config. voiceGateForAPIKeys must let those requests reach
// next directly, while still applying the wrapped gate in full to
// JWT-session (OWUI/web-console) callers.
func TestVoiceGateForAPIKeysBypassesGateForAPIKeyCallers(t *testing.T) {
	denyGate := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		})
	}
	inner := testRouteHandler("audio")
	gated := voiceGateForAPIKeys(denyGate)(inner)

	t.Run("api key caller bypasses the gate", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
		req.Header.Set("Authorization", "Bearer hk_test")
		rec := httptest.NewRecorder()

		gated.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("api-key caller: got status %d, want 204 (gate bypassed, inner handler reached)", rec.Code)
		}
	})

	t.Run("jwt session caller is still gated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", nil)
		req = req.WithContext(auth.WithUser(req.Context(), &auth.User{TenantID: uuid.New()}))
		rec := httptest.NewRecorder()

		gated.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("jwt caller: got status %d, want 403 (gate still enforced)", rec.Code)
		}
	})
}

// stubShimKeyResolver is a canned authz resolver for the OWUI shim-key probe.
// The canned answer is guarded because the recovery test swaps it while the
// watcher goroutine is already probing.
type stubShimKeyResolver struct {
	mu       sync.RWMutex
	snapshot authz.AuthSnapshot
	err      error
	calls    atomic.Int64
	seen     chan struct{}
}

// set replaces the canned answer. Callers that only build the stub before any
// probe starts can keep using a struct literal.
func (s *stubShimKeyResolver) set(snapshot authz.AuthSnapshot, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot, s.err = snapshot, err
}

func (s *stubShimKeyResolver) Resolve(_ context.Context, _ string) (authz.AuthSnapshot, error) {
	s.calls.Add(1)
	if s.seen != nil {
		select {
		case s.seen <- struct{}{}:
		default:
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot, s.err
}

func TestCheckOWUIShimKeyClassifiesEveryUnusableState(t *testing.T) {
	cases := []struct {
		name     string
		resolver *stubShimKeyResolver
		wantErr  string
	}{
		{
			name:     "unregistered or revoked key does not resolve",
			resolver: &stubShimKeyResolver{err: errors.New("authz: resolve status 404: not found")},
			wantErr:  "does not resolve",
		},
		{
			name:     "resolved key is not active",
			resolver: &stubShimKeyResolver{snapshot: authz.AuthSnapshot{Status: "revoked", AllowAllModels: true}},
			wantErr:  `status "revoked"`,
		},
		{
			name: "resolved key permits no models",
			resolver: &stubShimKeyResolver{snapshot: authz.AuthSnapshot{
				Status: "active", TenantID: uuid.New().String(),
			}},
			wantErr: "allowed no models",
		},
		{
			name: "resolved key's account resolves no tenant",
			resolver: &stubShimKeyResolver{snapshot: authz.AuthSnapshot{
				Status: "active", AllowAllModels: true,
			}},
			wantErr: "not provisioned",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkOWUIShimKey(context.Background(), tc.resolver, testOWUIShimKey)
			if err == nil {
				t.Fatalf("expected an error naming the cause, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestCheckOWUIShimKeyAcceptsAResolvableKey(t *testing.T) {
	for _, tc := range []struct {
		name     string
		snapshot authz.AuthSnapshot
	}{
		{"allow all models", authz.AuthSnapshot{
			Status: "active", AllowAllModels: true, TenantID: uuid.New().String(),
		}},
		{"explicit allowlist", authz.AuthSnapshot{
			Status: "active", AllowedAliases: []string{"hive-embedding-default"}, TenantID: uuid.New().String(),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &stubShimKeyResolver{snapshot: tc.snapshot}
			if err := checkOWUIShimKey(context.Background(), resolver, testOWUIShimKey); err != nil {
				t.Fatalf("expected a healthy verdict, got %v", err)
			}
		})
	}
}

// TestCheckOWUIShimKeyDistinguishesUpstreamUnavailableFromDoesNotResolve
// guards the 2026-08-14 near-miss: a resolve call that never reached a
// verdict on the key (control-plane timeout/cold-start) must not read the
// same as "this key does not resolve", or the operator is told to rotate a
// key that is perfectly fine.
func TestCheckOWUIShimKeyDistinguishesUpstreamUnavailableFromDoesNotResolve(t *testing.T) {
	resolver := &stubShimKeyResolver{
		err: fmt.Errorf("authz: fetch: %w: %w", authz.ErrUpstreamUnavailable, context.DeadlineExceeded),
	}

	err := checkOWUIShimKey(context.Background(), resolver, testOWUIShimKey)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "does not resolve to a Hive API key") {
		t.Fatalf("a transient upstream failure must not read as the key not resolving, got %q", err.Error())
	}
	if !errors.Is(err, authz.ErrUpstreamUnavailable) {
		t.Fatalf("expected the upstream-unavailable cause preserved through the wrap, got %q", err.Error())
	}
}

// TestWatchOWUIShimKeyLogsTransientUnavailableWithoutRotateAdvice pins the log
// line an operator actually reads: on an upstream-unavailable verdict it must
// not carry the "mint a replacement" remedy, since rotating the key would
// achieve nothing (the key was never the problem) and could break a working
// long-lived deployment if acted on.
func TestWatchOWUIShimKeyLogsTransientUnavailableWithoutRotateAdvice(t *testing.T) {
	logged := captureLog(t)
	resolver := &stubShimKeyResolver{
		err: fmt.Errorf("authz: fetch: %w: %w", authz.ErrUpstreamUnavailable, context.DeadlineExceeded),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		watchOWUIShimKey(ctx, resolver, testOWUIShimKey, time.Hour)
		close(done)
	}()
	waitFor(t, func() bool { return strings.Contains(logged.String(), "OWUI_SHIM_KEY") })
	cancel()
	<-done

	out := logged.String()
	if strings.Contains(out, "Mint a replacement") {
		t.Fatalf("transient upstream-unavailable must not advise rotating the key, got %q", out)
	}
	for _, want := range []string{"WARN", "transient", "do not rotate"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected the log to contain %q, got %q", want, out)
		}
	}
}

// TestWatchOWUIShimKeyLogsDeadKeyAfterTransientFailure is the regression
// guard for the PR #903 security review MEDIUM finding: watchOWUIShimKey used
// to compare a plain boolean "healthy" against its last-logged value, so a
// transient upstream-unavailable failure followed by a genuinely dead key
// never re-logged -- both collapsed to the same "unhealthy" boolean, so the
// second, more actionable verdict (the one with the "mint a replacement"
// remedy) was silently dropped. Asserts both verdicts reach the log.
func TestWatchOWUIShimKeyLogsDeadKeyAfterTransientFailure(t *testing.T) {
	logged := captureLog(t)
	resolver := &stubShimKeyResolver{
		err:  fmt.Errorf("authz: fetch: %w: %w", authz.ErrUpstreamUnavailable, context.DeadlineExceeded),
		seen: make(chan struct{}, 1),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		watchOWUIShimKey(ctx, resolver, testOWUIShimKey, time.Millisecond)
		close(done)
	}()
	waitFor(t, func() bool { return strings.Contains(logged.String(), "WARN") })

	// Same boolean "unhealthy" as the transient failure above, but a
	// different, more actionable verdict: this key is actually dead, not
	// merely unreachable right now. Must produce its own log line.
	resolver.set(authz.AuthSnapshot{}, errors.New("authz: resolve status 404: not found"))
	waitFor(t, func() bool { return strings.Contains(logged.String(), "ERROR") })
	cancel()
	<-done

	out := logged.String()
	if !strings.Contains(out, "Mint a replacement") {
		t.Fatalf("expected the genuinely-dead verdict to reach the log with its remedy, got %q", out)
	}
}

// TestWatchOWUIShimKeyReportsAnUnusableKey is the alarm that replaces the empty
// model picker. A dead shim key must reach the operator log with the cause and
// the remedy named, not just fail three features quietly.
func TestWatchOWUIShimKeyReportsAnUnusableKey(t *testing.T) {
	logged := captureLog(t)
	resolver := &stubShimKeyResolver{err: errors.New("authz: resolve status 404: not found")}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		watchOWUIShimKey(ctx, resolver, testOWUIShimKey, time.Hour)
		close(done)
	}()
	waitFor(t, func() bool { return strings.Contains(logged.String(), "OWUI_SHIM_KEY") })
	cancel()
	<-done

	out := logged.String()
	for _, want := range []string{"ERROR", "OWUI_SHIM_KEY", "does not resolve", "scripts/seed-owui-e2e-user.py"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected the log to contain %q, got %q", want, out)
		}
	}
}

// TestWatchOWUIShimKeyLogsRecovery proves the alarm clears, so an operator can
// tell a fixed key from an unmonitored one.
func TestWatchOWUIShimKeyLogsRecovery(t *testing.T) {
	logged := captureLog(t)
	resolver := &stubShimKeyResolver{
		err:  errors.New("authz: resolve status 404: not found"),
		seen: make(chan struct{}, 1),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		watchOWUIShimKey(ctx, resolver, testOWUIShimKey, time.Millisecond)
		close(done)
	}()
	waitFor(t, func() bool { return strings.Contains(logged.String(), "ERROR") })
	resolver.set(authz.AuthSnapshot{Status: "active", AllowAllModels: true, TenantID: uuid.New().String()}, nil)
	waitFor(t, func() bool { return strings.Contains(logged.String(), "resolves to an active Hive API key") })
	cancel()
	<-done
}

// TestWatchOWUIShimKeyIsANoOpWhenUnset keeps deployments with no Open WebUI
// front-end silent, and must not probe at all.
func TestWatchOWUIShimKeyIsANoOpWhenUnset(t *testing.T) {
	for _, shimKey := range []string{"", "   "} {
		resolver := &stubShimKeyResolver{}
		watchOWUIShimKey(context.Background(), resolver, shimKey, time.Millisecond)
		if got := resolver.calls.Load(); got != 0 {
			t.Fatalf("expected no probe for shim key %q, got %d calls", shimKey, got)
		}
	}
}

// captureLog redirects the standard logger for one test and returns the buffer.
func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return buf
}

// syncBuffer is a mutex-guarded bytes.Buffer: the watcher writes from its own
// goroutine while the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within 2s")
}
