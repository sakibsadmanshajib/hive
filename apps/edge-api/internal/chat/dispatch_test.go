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
	"github.com/hivegpt/hive/apps/edge-api/internal/auth"
	"github.com/hivegpt/hive/apps/edge-api/internal/chat"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

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
