package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/chat"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/packages/dbtest"
	"github.com/stretchr/testify/require"
)

// newPassthroughRoutingClient returns a RoutingClient backed by a fake
// control-plane that resolves any alias to itself as the LiteLLM model
// name. These tests exercise dispatch's trace/audit/provider-blind
// behaviour, not routing resolution itself (covered in
// internal/inference), so a passthrough keeps request bodies unchanged.
func newPassthroughRoutingClient(t *testing.T) *inference.RoutingClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in inference.SelectRouteInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		_ = json.NewEncoder(w).Encode(inference.SelectRouteResult{
			AliasID:          in.AliasID,
			LiteLLMModelName: in.AliasID,
			Provider:         "test-provider",
			// hive-fast's catalog row as of migration
			// 20260801_01_alias_pricing_correction.sql, pinned on purpose
			// rather than tracked against later repricings: the real endpoint
			// always sends a price and an explicit unit, and the recorded cost
			// is derived from them (#688).
			Pricing:   inference.FixedPricing(10_500, 42_000),
			PriceUnit: inference.PriceUnitTokens,
		})
	}))
	t.Cleanup(srv.Close)
	return inference.NewRoutingClient(srv.URL)
}

func TestDispatchHappyPathWritesLLMTraceAndAuditsChatRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1,\"total_tokens\":4}}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer upstream.Close()

	tenantID := uuid.New()
	userID := uuid.New()
	accounting, billing := billedDeps(t)
	handler := chat.NewDispatch(chat.Deps{
		Pool:       pool,
		Routing:    newPassthroughRoutingClient(t),
		Accounting: accounting,
		Billing:    billing,
		LiteLLMURL: upstream.URL,
		DeploySHA:  "test",
		Env:        "test",
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID:       userID,
		TenantID: tenantID,
		Role:     "member",
		Email:    "x@y.example",
	}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "data: ")
	require.Contains(t, rec.Body.String(), "[DONE]")

	var traceCount int
	var costCredits int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*), coalesce(max(cost_credits), 0) FROM public.llm_traces WHERE tenant_id=$1 AND user_id=$2`,
		tenantID,
		userID,
	).Scan(&traceCount, &costCredits))
	require.Equal(t, 1, traceCount)
	// 3 input + 1 output tokens at the alias's catalog price rounds below one
	// credit, so the never-free floor makes it 1. It is not 4, the token total
	// this used to record (#688).
	require.Equal(t, int64(1), costCredits)

	var actions []string
	rows, err := pool.Query(ctx, `SELECT action FROM public.audit_log WHERE tenant_id=$1`, tenantID)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var action string
		require.NoError(t, rows.Scan(&action))
		actions = append(actions, action)
	}
	require.Contains(t, actions, "CHAT_REQUEST")
}

func TestDispatchNoTenantReturnsNoTenant(t *testing.T) {
	handler := chat.NewDispatch(chat.Deps{LiteLLMURL: "http://unused", DeploySHA: "s", Env: "test"})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{}`)))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: uuid.New()}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)

	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(io.NopCloser(bytes.NewReader(rec.Body.Bytes()))).Decode(&errBody))
	require.Equal(t, "NO_TENANT", errBody.Error.Code)
}

// TestDispatchUpstreamErrorIsProviderBlind covers the regulated path where
// the upstream returns a 4xx/5xx body containing provider names. The
// customer-visible response must not contain any provider identifier
// (openrouter, groq, openai, anthropic) or route slug — the BD market
// regulatory guarantee requires every wire-format error to be
// provider-blind.
func TestDispatchUpstreamErrorIsProviderBlind(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"route-groq-fast hit groq rate limits via openrouter/auto"}}`))
	}))
	defer upstream.Close()

	accounting, billing := billedDeps(t)
	handler := chat.NewDispatch(chat.Deps{
		Routing:    newPassthroughRoutingClient(t),
		Accounting: accounting,
		Billing:    billing,
		LiteLLMURL: upstream.URL,
		DeploySHA:  "test",
		Env:        "test",
	})

	// Mock upstream — no real LiteLLM/OpenRouter call is made. Request body
	// uses the project's default Groq alias so the test never references the
	// live billing route, even in logs.
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"groq/openai/gpt-oss-20b","messages":[{"role":"user","content":"hi"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID:       uuid.New(),
		TenantID: uuid.New(),
		Role:     "member",
		Email:    "x@y.example",
	}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)

	body := strings.ToLower(rec.Body.String())
	for _, leak := range []string{"openrouter", "groq", "openai", "anthropic", "route-"} {
		require.NotContains(t, body, leak, "provider-blind violation: %q leaked through dispatch", leak)
	}
}

// TestDispatchResolvesAliasToLiteLLMModelName is the #269 regression guard:
// LiteLLM's model_list only contains route names (e.g. "route-groq-fast"),
// never Hive catalog aliases (e.g. "hive-fast"). Before this fix, dispatch
// forwarded the alias straight through and LiteLLM 400'd with "Invalid
// model name passed in model=hive-fast" on every real OWUI/web-console
// chat request.
func TestDispatchResolvesAliasToLiteLLMModelName(t *testing.T) {
	var gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	routing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(inference.SelectRouteResult{
			AliasID:          "hive-fast",
			LiteLLMModelName: "route-groq-fast",
			Provider:         "groq",
			Pricing:          inference.FixedPricing(10_500, 42_000),
			PriceUnit:        inference.PriceUnitTokens,
		})
	}))
	defer routing.Close()

	accounting, billing := billedDeps(t)
	handler := chat.NewDispatch(chat.Deps{
		Routing:    inference.NewRoutingClient(routing.URL),
		Accounting: accounting,
		Billing:    billing,
		LiteLLMURL: upstream.URL,
		DeploySHA:  "test",
		Env:        "test",
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID: uuid.New(), TenantID: uuid.New(), Role: "member",
	}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "route-groq-fast", gotModel)
}

// TestDispatchFixedPriceStreamSanitizesUpstreamID reproduces the security
// review finding on PR #1222: this relay's fixed-price branch used to write
// every SSE line verbatim, with no sanitization at all. Fixed-price is the
// D-032 norm for most aliases, so this is the primary Open WebUI chat
// surface, not an edge case. The fixture id/system_fingerprint shape below
// matches the live leak captured in the PR (OpenRouter gen-*, Groq
// chatcmpl-*+system_fingerprint).
func TestDispatchFixedPriceStreamSanitizesUpstreamID(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte(`data: {"id":"chatcmpl-8f3a9c2e1b4d","object":"chat.completion.chunk","system_fingerprint":"fp_44709d6fcb","model":"route-groq-fast","choices":[{"index":0,"delta":{"content":"hi"}}]}` + "\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte(`data: {"id":"chatcmpl-8f3a9c2e1b4d","object":"chat.completion.chunk","system_fingerprint":"fp_44709d6fcb","model":"route-groq-fast","choices":[{"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}` + "\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer upstream.Close()

	routing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(inference.SelectRouteResult{
			AliasID:          "hive-fast",
			LiteLLMModelName: "route-groq-fast",
			Provider:         "groq",
			// Fixed pricing (D-032 norm) is the branch that shipped raw
			// before this fix -- IsUpstreamActual() must be false here.
			Pricing:   inference.FixedPricing(10_500, 42_000),
			PriceUnit: inference.PriceUnitTokens,
		})
	}))
	defer routing.Close()

	accounting, billing := billedDeps(t)
	handler := chat.NewDispatch(chat.Deps{
		Routing:    inference.NewRoutingClient(routing.URL),
		Accounting: accounting,
		Billing:    billing,
		LiteLLMURL: upstream.URL,
		DeploySHA:  "test",
		Env:        "test",
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID: uuid.New(), TenantID: uuid.New(), Role: "member",
	}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	require.NotContains(t, body, "chatcmpl-8f3a9c2e1b4d", "upstream id leaked on a fixed-price stream")
	require.NotContains(t, body, "system_fingerprint", "system_fingerprint leaked on a fixed-price stream")
	require.NotContains(t, body, "fp_44709d6fcb", "system_fingerprint value leaked on a fixed-price stream")

	idPrefix := `"id":"`
	start := strings.Index(body, idPrefix)
	require.NotEqual(t, -1, start, "expected a minted id in the sanitized stream:\n%s", body)
	start += len(idPrefix)
	mintedID := body[start : start+strings.Index(body[start:], `"`)]
	require.True(t, strings.HasPrefix(mintedID, "chatcmpl-"), "minted id %q should carry the chatcmpl- prefix", mintedID)
	require.Equal(t, 2, strings.Count(body, mintedID), "expected the SAME minted id on both chunks of one stream")
}

// TestDispatchUnknownAliasReturns404 covers an alias the catalog does not
// recognise -- the request must never reach LiteLLM with an unresolved
// model string.
func TestDispatchUnknownAliasReturns404(t *testing.T) {
	routing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer routing.Close()

	handler := chat.NewDispatch(chat.Deps{
		Routing:    inference.NewRoutingClient(routing.URL),
		LiteLLMURL: "http://unused",
		DeploySHA:  "test",
		Env:        "test",
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"not-a-real-alias","messages":[{"role":"user","content":"hi"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID: uuid.New(), TenantID: uuid.New(), Role: "member",
	}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestDispatchRoutingBackendFailureReturns503 covers a control-plane
// failure that is NOT "alias not found" (e.g. an internal error) -- this
// must surface as 503, not 404, so a transient routing outage is never
// misreported as a missing model (#289 review).
func TestDispatchRoutingBackendFailureReturns503(t *testing.T) {
	routing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer routing.Close()

	handler := chat.NewDispatch(chat.Deps{
		Routing:    inference.NewRoutingClient(routing.URL),
		LiteLLMURL: "http://unused",
		DeploySHA:  "test",
		Env:        "test",
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID: uuid.New(), TenantID: uuid.New(), Role: "member",
	}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestDispatchUnentitledModelReturns403 covers the tenant entitlement refusal.
// It must not fall into the generic branch, which would report an admin policy
// decision as a transient 503 routing outage.
func TestDispatchUnentitledModelReturns403(t *testing.T) {
	routing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"routing: model not entitled for tenant: alias hive-fast"}`))
	}))
	defer routing.Close()

	handler := chat.NewDispatch(chat.Deps{
		Routing:    inference.NewRoutingClient(routing.URL),
		LiteLLMURL: "http://unused",
		DeploySHA:  "test",
		Env:        "test",
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID: uuid.New(), TenantID: uuid.New(), Role: "member",
	}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
	// The refusal names only the requested model; it must not enumerate what
	// other tenants can see.
	require.NotContains(t, rec.Body.String(), "route-")
}

func newPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := dbtest.RequireURL(t, "HIVE_TEST_DB_URL")
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool
}
