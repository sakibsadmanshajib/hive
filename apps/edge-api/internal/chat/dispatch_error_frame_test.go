package chat_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/chat"
	"github.com/stretchr/testify/require"
)

// TestDispatchMidStreamErrorFrameIsRenderedNotDropped covers an upstream that
// answers 200 and then puts its failure inside the SSE body, which is what
// LiteLLM's proxy does once a streaming response has committed its status.
//
// What changes here is not leakage. packages/sanitize already refuses a frame
// carrying a top-level error, and this relay already dropped what it refused,
// so nothing upstream reached the customer before this change either
// (measured against origin/main, PR #1303 review). What the customer got was
// a half-written answer followed by a normal end of stream, with nothing to
// render and nothing to retry on: a silent truncation. The relay now answers
// with a gateway-owned error frame instead.
//
// The forbidden-substring assertions below therefore pin the property that
// must SURVIVE the change rather than one the change introduces: the
// replacement is built from scratch, so no amount of upstream prose in the
// input may appear in the output. They go red the moment someone tries to
// forward or scrub the upstream error instead of replacing it.
//
// The fixtures are shapes seen in the wild, not invented ones.
func TestDispatchMidStreamErrorFrameIsRenderedNotDropped(t *testing.T) {
	cases := []struct {
		name      string
		frame     string
		forbidden []string
	}{
		{
			name:  "litellm relaying an upstream quota refusal",
			frame: `data: {"error":{"message":"litellm.RateLimitError: RateLimitError: OpenrouterException - {\"error\":{\"message\":\"You exceeded your current quota, please check your plan and billing details\",\"code\":429}}","type":"insufficient_quota","param":null,"code":"429"}}`,
			forbidden: []string{
				"exceeded your current quota",
				"plan and billing",
				"openrouter",
				"litellm",
				"ratelimiterror",
				"insufficient_quota",
			},
		},
		{
			// Captured, not invented: the exact frame LiteLLM v1.98.0 (the
			// image pinned in deploy/docker/docker-compose.yml) emits when the
			// upstream fails after generation has started, measured against a
			// fake upstream that streams one content chunk and then refuses.
			// The proxy answers 200, so no status classification can ever
			// catch this one.
			name:  "measured litellm frame after generation started",
			frame: `data: {"error": {"message": "litellm.APIConnectionError: APIConnectionError: OpenAIException - You exceeded your current quota, please check your plan and billing details.", "type": null, "param": null, "code": "500"}}`,
			forbidden: []string{
				"exceeded your current quota",
				"plan and billing",
				"litellm",
				"openaiexception",
				"apiconnectionerror",
			},
		},
		{
			name:  "provider out of credit with a top-up link",
			frame: `data: {"error":{"message":"Insufficient credits. Add more using https://openrouter.ai/settings/credits","code":402}}`,
			forbidden: []string{
				"insufficient credits",
				"openrouter",
				"settings/credits",
				"https://",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				flusher := w.(http.Flusher)
				_, _ = w.Write([]byte(tc.frame + "\n\n"))
				flusher.Flush()
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				flusher.Flush()
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
				strings.NewReader(`{"model":"hive-auto","messages":[{"role":"user","content":"hi"}]}`),
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

			require.Equal(t, http.StatusOK, rec.Code)
			body := rec.Body.String()
			lower := strings.ToLower(body)
			for _, forbidden := range tc.forbidden {
				require.NotContainsf(t, lower, forbidden,
					"upstream error text reached the customer: %q in %q", forbidden, body)
			}
			// The point of the change: the client gets something to render.
			// This half goes red on main, where the frame is dropped and the
			// customer sees only [DONE].
			require.Contains(t, body, "hive-auto")
			require.Contains(t, body, `"code":"upstream_error"`)
			require.Contains(t, body, "is unavailable right now")
			require.Contains(t, body, "[DONE]")
		})
	}
}

// TestDispatchUnparseableFrameIsStillDropped keeps the other branch honest:
// only a frame that actually carries an error becomes a rendered error.
// Garbage stays dropped, so a mangled chunk cannot manufacture a failure on a
// stream that is otherwise fine.
func TestDispatchUnparseableFrameIsStillDropped(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {not json at all\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
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
		strings.NewReader(`{"model":"hive-auto","messages":[{"role":"user","content":"hi"}]}`),
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

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.NotContains(t, body, "not json at all")
	require.NotContains(t, body, "upstream_error")
}
