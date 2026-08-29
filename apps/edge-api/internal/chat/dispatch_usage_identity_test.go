package chat_test

// Session chat is the third surface issue #1472's rule has to hold on, and it
// is the one that serves the Open WebUI front end. It was the gap: this relay
// writes every sanitized frame straight to the client and only afterwards
// decodes one for billing, so a total_tokens that disagreed with its own
// components reached a chat customer verbatim while /v1/chat/completions,
// /v1/completions, /v1/responses and /v1/embeddings were all corrected.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/chat"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/stretchr/testify/require"
)

// TestDispatchStreamHoldsTheUsageFrameToTheIdentity asserts the number the
// customer receives, on the exact live shape from #1472 (total 31 against
// components summing to 5), and asserts alongside it that the components were
// NOT rewritten. Correcting a disagreement by inflating completion_tokens
// would satisfy the identity and start billing a class that has never been
// billed, which D-055 forbids; an assertion on the total alone would pass
// through that.
func TestDispatchStreamHoldsTheUsageFrameToTheIdentity(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte(`data: {"id":"gen-abc","object":"chat.completion.chunk","model":"route-groq-fast","choices":[{"index":0,"delta":{"content":"hi"}}]}` + "\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte(`data: {"id":"gen-abc","object":"chat.completion.chunk","model":"route-groq-fast","choices":[{"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":31,"completion_tokens_details":{"reasoning_tokens":26}}}` + "\n\n"))
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

	usage := relayedUsageBlock(t, rec.Body.String())
	require.Equal(t, int64(4), usage.PromptTokens, "prompt_tokens must be untouched: the charge prices it")
	require.Equal(t, int64(1), usage.CompletionTokens, "completion_tokens must be untouched: inflating it would bill a class that has never been billed (D-055)")
	require.Equal(t, int64(5), usage.TotalTokens, "the customer received a total that disagrees with its own components")
	require.NotNil(t, usage.CompletionTokensDetails, "the breakdown must survive the correction")
	require.LessOrEqual(t, usage.CompletionTokensDetails.ReasoningTokens, usage.CompletionTokens,
		"a breakdown may not claim more reasoning tokens than the component it breaks down")
}

// relayedUsageBlock returns the usage block of the one relayed SSE frame that
// carries one, failing if the stream carried none. Reading it back off the
// wire rather than off a handler internal is the point: this is the byte
// sequence the customer's client parses.
func relayedUsageBlock(t *testing.T, wire string) *inference.UsageResponse {
	t.Helper()
	for _, line := range strings.Split(wire, "\n") {
		line = strings.TrimSpace(line)
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok || payload == "[DONE]" {
			continue
		}
		var chunk inference.ChatCompletionChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("relayed frame is not valid JSON: %s", payload)
		}
		if chunk.Usage != nil {
			return chunk.Usage
		}
	}
	t.Fatalf("no usage frame reached the client; wire:\n%s", wire)
	return nil
}
