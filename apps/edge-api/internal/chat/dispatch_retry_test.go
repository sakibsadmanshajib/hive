package chat_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/chat"
	"github.com/stretchr/testify/require"
)

// TestDispatchRetriesUpstream429ThenSucceeds is the regression guard for
// issue #1564. The browser/JWT chat surface used to make a single bare HTTP
// call to LiteLLM with no retry at all, while the API-key surface wraps the
// same call in dispatchWithRetry's four-attempt ladder
// (internal/inference/retry.go). LiteLLM's router_settings.retry_policy sets
// RateLimitErrorRetries to 0 on the assumption that edge-api retries a 429
// itself, so a JWT chat send that happened to draw an exhausted free-pool
// member (out of several healthy ones) failed outright with "hive-free is
// not available" instead of trying a different pool member. This test proves
// the session chat surface now retries a 429 and succeeds on a later
// attempt, the same way the API-key surface always has.
func TestDispatchRetriesUpstream429ThenSucceeds(t *testing.T) {
	var attempts int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited: no fallback model group found"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
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

	require.Equal(t, http.StatusOK, rec.Code,
		"expected the first 429 to be retried and the later 200 to be returned, body=%s", rec.Body.String())
	require.GreaterOrEqual(t, int(atomic.LoadInt32(&attempts)), 2,
		"expected the handler to retry the upstream after the first 429")
}
