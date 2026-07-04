package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/chat"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
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
	handler := chat.NewDispatch(chat.Deps{
		Pool:       pool,
		Routing:    newPassthroughRoutingClient(t),
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
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM public.llm_traces WHERE tenant_id=$1 AND user_id=$2`,
		tenantID,
		userID,
	).Scan(&traceCount))
	require.Equal(t, 1, traceCount)

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

	handler := chat.NewDispatch(chat.Deps{
		Routing:    newPassthroughRoutingClient(t),
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
		})
	}))
	defer routing.Close()

	handler := chat.NewDispatch(chat.Deps{
		Routing:    inference.NewRoutingClient(routing.URL),
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

func newPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool
}
