package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- issue #1226: streams never request upstream usage ---
//
// Half 1 of #1226: the direct inference streaming path
// (/v1/chat/completions, /v1/completions) never forced
// stream_options.include_usage on the outbound request, so a caller that
// never set the flag itself got no terminal usage frame from a provider that
// requires it, and settlement fell through to #1215's fail-closed hold
// capture far more than it needed to. These tests prove the gateway now
// requests usage itself on a verified provider, leaves an unverified
// provider untouched, and never lets the caller-invisible flag change what
// the caller itself receives.

// capturingCompletingSSEServer behaves like completingSSEServer (content,
// stop, a terminal usage chunk, [DONE]) and additionally records the raw
// request body edge-api sent it, so a test can assert the outbound
// stream_options shape rather than only the relayed response.
func capturingCompletingSSEServer(capturedBody *[]byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		*capturedBody = raw
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		stop := "stop"
		fmt.Fprintln(w, buildChunkLine("chunk-1", "route", "hello there", nil))
		flusher.Flush()
		fmt.Fprintln(w, buildChunkLine("chunk-2", "route", "", &stop))
		flusher.Flush()
		usageChunk := ChatCompletionChunk{
			ID:      "chunk-3",
			Object:  "chat.completion.chunk",
			Created: 1700000000,
			Model:   "route",
			Choices: []ChunkChoice{},
			Usage: &UsageResponse{
				PromptTokens:     20,
				CompletionTokens: 5,
				TotalTokens:      25,
			},
		}
		b, _ := json.Marshal(usageChunk)
		fmt.Fprintln(w, "data: "+string(b))
		flusher.Flush()
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// newRoutingMockProvider is newRoutingMock with a caller-chosen provider, so
// a test can exercise the per-provider include_usage gate against both a
// verified provider and an unverified one.
func newRoutingMockProvider(litellmURL, provider string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(SelectRouteResult{
			AliasID:          "gpt-4o",
			RouteID:          "route-test-1",
			LiteLLMModelName: "litellm-route",
			Provider:         provider,
			Pricing:          catalogHiveFast,
			PriceUnit:        PriceUnitTokens,
		})
	}))
}

// TestExecuteStreaming_ForcesIncludeUsage_SupportedProvider is the wiring-gap
// RED test: the caller's own request never sets stream_options.include_usage
// (runExecuteStreaming's harness always sends a bare `{}` body), yet the
// gateway must still request usage on Groq and OpenRouter -- the two
// providers this catalog dispatches to today (owner decision 2026-08-26,
// DeepSeek routes through OpenRouter).
func TestExecuteStreaming_ForcesIncludeUsage_SupportedProvider(t *testing.T) {
	for _, provider := range []string{"groq", "openrouter"} {
		t.Run(provider, func(t *testing.T) {
			rec := &accountingRecorder{}
			acctSrv := newAccountingMock(rec)
			defer acctSrv.Close()

			var capturedBody []byte
			litellmSrv := capturingCompletingSSEServer(&capturedBody)
			defer litellmSrv.Close()

			routingSrv := newRoutingMockProvider(litellmSrv.URL, provider)
			defer routingSrv.Close()

			orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

			done, _ := runExecuteStreaming(orch, context.Background())
			waitDone(t, done)

			var outbound struct {
				StreamOptions struct {
					IncludeUsage bool `json:"include_usage"`
				} `json:"stream_options"`
			}
			if err := json.Unmarshal(capturedBody, &outbound); err != nil {
				t.Fatalf("decode outbound body: %v (body=%s)", err, capturedBody)
			}
			if !outbound.StreamOptions.IncludeUsage {
				t.Errorf("provider=%s: outbound stream_options.include_usage = false, want true; the client never set it, so the gateway must force it itself (#1226)", provider)
			}
		})
	}
}

// TestExecuteStreaming_DoesNotForceIncludeUsage_UnsupportedProvider is the
// fail-safe complement: an upstream not on the verified list keeps the
// caller's own (empty) body untouched, since not every provider accepts the
// flag and an unsupported one can break the request.
func TestExecuteStreaming_DoesNotForceIncludeUsage_UnsupportedProvider(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	var capturedBody []byte
	litellmSrv := capturingCompletingSSEServer(&capturedBody)
	defer litellmSrv.Close()

	routingSrv := newRoutingMockProvider(litellmSrv.URL, "some-future-provider")
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	done, _ := runExecuteStreaming(orch, context.Background())
	waitDone(t, done)

	if bytes.Contains(capturedBody, []byte("include_usage")) {
		t.Errorf("outbound body forced include_usage for an unverified provider: %s", capturedBody)
	}
}

// TestExecuteStreaming_ClientDidNotAskForUsage_NeverSeesUsageFrame is the
// contract-neutrality guard: forcing include_usage upstream for billing must
// never change what a caller who did not opt in receives. Usage is still
// consumed at settlement (confirmed, exact catalog price), but the frame
// that carried it is dropped before it reaches the caller.
func TestExecuteStreaming_ClientDidNotAskForUsage_NeverSeesUsageFrame(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	litellmSrv := completingSSEServer()
	defer litellmSrv.Close()

	routingSrv := newRoutingMockProvider(litellmSrv.URL, "groq")
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := newHeaderCommitRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		// includeUsage=false: the caller's own request never asked for it.
		_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o", "gpt-4o",
			NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, 10000, false, nil, orch.litellm.ChatCompletion)
	}()
	waitDone(t, done)

	if strings.Contains(w.Body.String(), `"usage"`) {
		t.Errorf("client never asked for stream_options.include_usage but received a usage field in its stream: %s", w.Body.String())
	}

	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation; calls seen: %+v", rec.calls)
	}
	if confirmed, _ := body["terminal_usage_confirmed"].(bool); !confirmed {
		t.Error("terminal_usage_confirmed must be true: billing still consumed the real usage block even though the client never saw it")
	}
	actual, _ := body["actual_credits"].(float64)
	if int64(actual) != 1 {
		t.Errorf("actual_credits = %v, want 1 (exact catalog price for 25 confirmed tokens, floored) -- not the reservation hold", body["actual_credits"])
	}
}
