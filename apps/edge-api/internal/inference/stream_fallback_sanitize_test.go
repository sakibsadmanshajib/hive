package inference

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- PR #1222 security review round 2, blocking finding 1 ---
//
// executeStreaming's typed decode fails on some upstream frames (the
// DeepSeek-family surprise-frame class covered by unparseableFrameSSEServer
// in stream_usage_missing_test.go). Before the round-2 fix, that fallback
// branched on route.Pricing.IsUpstreamActual(): a variable-price route ran
// the frame through SanitizeVariablePriceFrame, but a FIXED-price route
// (the D-032 norm for most aliases) wrote the raw upstream line verbatim,
// id/system_fingerprint and all, with no sanitization and no log line. The
// fix collapsed both branches onto the one sanitizer, unconditionally.
//
// This test exercises the real executeStreaming call site end to end (not a
// hand-simulated reimplementation) against a fixed-price route, so a revert
// of the collapse back to the old two-branch shape fails it.

// decodeFailureFixedPriceSSEServer streams one frame that fails typed
// decode into ChatCompletionChunk (model typed as a number where a string
// is declared) and carries an upstream id + system_fingerprint in the exact
// shape the live leak had (OpenRouter gen-* id, PR #1222 evidence), then
// [DONE].
func decodeFailureFixedPriceSSEServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintln(w, `data: {"id":"gen-1787734329-THDkSN71nG5F8uAbPdnB","object":"chat.completion.chunk","created":1700000000,"model":42,"system_fingerprint":"fp_deadbeef1234","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`)
		flusher.Flush()
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// TestExecuteStreaming_FixedPriceDecodeFailureFallback_SanitizesUpstreamIdentity
// is the regression guard for blocking finding 1: a decode-failure frame on
// a FIXED-price route (catalogHiveFast, via newRoutingMock -- the branch
// that shipped raw before the round-2 fix) must never leak the upstream id
// or system_fingerprint to the client, and must carry a gateway-minted id
// instead.
func TestExecuteStreaming_FixedPriceDecodeFailureFallback_SanitizesUpstreamIdentity(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	litellmSrv := decodeFailureFixedPriceSSEServer()
	defer litellmSrv.Close()

	// catalogHiveFast (settle_from_catalog_test.go) is FixedPricing:
	// IsUpstreamActual() must be false here, exactly the branch that used
	// to skip sanitization entirely.
	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := newHeaderCommitRecorder()
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o", "gpt-4o",
			NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, 10000, false, nil, orch.litellm.ChatCompletion)
	}()
	waitDone(t, doneCh)

	body := w.Body.String()

	if strings.Contains(body, "gen-1787734329-THDkSN71nG5F8uAbPdnB") {
		t.Errorf("fixed-price decode-failure fallback leaked the raw upstream id verbatim:\n%s", body)
	}
	if strings.Contains(body, "system_fingerprint") {
		t.Errorf("fixed-price decode-failure fallback leaked the system_fingerprint key:\n%s", body)
	}
	if strings.Contains(body, "fp_deadbeef1234") {
		t.Errorf("fixed-price decode-failure fallback leaked the system_fingerprint value:\n%s", body)
	}
	if !strings.Contains(body, `"id":"chatcmpl-`) {
		t.Fatalf("expected a gateway-minted chatcmpl- id in the sanitized fallback output, got:\n%s", body)
	}
}
