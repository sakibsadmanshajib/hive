package chat_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/chat"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
)

// fakeMemories is a MemorySource returning canned contents, recording the
// (tenant, user, limit) it was asked for.
type fakeMemories struct {
	gotTenant uuid.UUID
	gotUser   uuid.UUID
	gotLimit  int
	contents  []string
	err       error
}

func (f *fakeMemories) Recent(_ context.Context, tenantID, userID uuid.UUID, limit int) ([]string, error) {
	f.gotTenant = tenantID
	f.gotUser = userID
	f.gotLimit = limit
	return f.contents, f.err
}

// captureUpstream returns an httptest server standing in for LiteLLM that
// records the forwarded body, mirroring the dispatch test harness style.
func captureUpstream(t *testing.T) (*httptest.Server, *[]byte) {
	t.Helper()
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

func TestDispatchInjectsMemoryBlockWhenMemoriesExist(t *testing.T) {
	srv, captured := captureUpstream(t)
	routing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(inference.SelectRouteResult{
			AliasID:          "hive-fast",
			LiteLLMModelName: "route-groq-fast",
			Provider:         "groq",
			Pricing:          inference.FixedPricing(10_500, 42_000),
			PriceUnit:        inference.PriceUnitTokens,
		})
	}))
	t.Cleanup(routing.Close)

	accounting, billing := billedDeps(t)
	memories := &fakeMemories{contents: []string{"prefers terse answers"}}
	handler := chat.NewDispatch(chat.Deps{
		Routing:    inference.NewRoutingClient(routing.URL),
		Accounting: accounting,
		Billing:    billing,
		Memories:   memories,
		LiteLLMURL: srv.URL,
		DeploySHA:  "test",
		Env:        "test",
	})

	userID, tenantID := uuid.New(), uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID: userID, TenantID: tenantID, Role: "member",
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Equal(t, tenantID, memories.gotTenant)
	require.Equal(t, userID, memories.gotUser)
	require.Equal(t, 5, memories.gotLimit)

	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(*captured, &body))
	require.Len(t, body.Messages, 2)
	require.Equal(t, "system", body.Messages[0].Role)
	require.Contains(t, body.Messages[0].Content, "Known about the user:")
	require.Contains(t, body.Messages[0].Content, "- prefers terse answers")
	require.Equal(t, "user", body.Messages[1].Role)
}

func TestDispatchNoMemoriesMeansNoBlock(t *testing.T) {
	srv, captured := captureUpstream(t)
	routing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(inference.SelectRouteResult{
			AliasID: "hive-fast", LiteLLMModelName: "route-groq-fast",
			Provider: "groq", Pricing: inference.FixedPricing(10_500, 42_000),
			PriceUnit: inference.PriceUnitTokens,
		})
	}))
	t.Cleanup(routing.Close)

	accounting, billing := billedDeps(t)
	handler := chat.NewDispatch(chat.Deps{
		Routing:    inference.NewRoutingClient(routing.URL),
		Accounting: accounting,
		Billing:    billing,
		LiteLLMURL: srv.URL,
		DeploySHA:  "test",
		Env:        "test",
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID: uuid.New(), TenantID: uuid.New(), Role: "member",
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(*captured, &body))
	require.Len(t, body.Messages, 1)
	require.Equal(t, "user", body.Messages[0].Role)
}
