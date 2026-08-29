package inference

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// --- issue #1411: upstream funds exhaustion ---
//
// These tests drive the REAL Orchestrator.executeSync / executeStreaming
// lifecycle end to end against in-process httptest stand-ins for the
// control-plane (routing + accounting) and LiteLLM, the same harness
// stream_disconnect_test.go and sync_settlement_test.go already use. Only
// the LiteLLM stand-in is faked here; the reservation, release, dispatch
// and sanitization code paths are the real production code.
//
// The upstream body below is OpenRouter's documented error envelope
// (https://openrouter.ai/docs/api_reference/errors-and-debugging.md,
// confirmed live 2026-08-29): the JSON shape is error.code plus
// error.message plus error.metadata, with the HTTP status equal to
// error.code. It is SIMULATED (an httptest.Server, never a call to a real
// provider): draining the real OpenRouter wallet to observe this would
// cause the exact outage this test exists to prevent.

const openRouterInsufficientCreditsBody = "{\"error\":{\"code\":402,\"message\":\"Insufficient credits. Add more using https://openrouter.ai/settings/credits\",\"metadata\":{\"error_type\":\"payment_required\"}}}"

// paymentRequiredServer stands in for LiteLLM/the upstream provider
// answering with OpenRouter's real 402 shape, and counts how many times it
// was hit so a test can confirm the bounded-retry path (429/5xx only) did
// not treat 402 as retryable.
func paymentRequiredServer(hits *int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(openRouterInsufficientCreditsBody))
	}))
}

func assertUpstreamUnavailableBody(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (upstream funding refusal must not read as the customer owing money)", w.Code, http.StatusServiceUnavailable)
	}
	var resp apierrors.OpenAIError
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not a valid OpenAI error envelope: %v, body=%s", err, w.Body.String())
	}
	if resp.Error.Code == nil || *resp.Error.Code != "upstream_unavailable" {
		t.Fatalf("code = %v, want upstream_unavailable", resp.Error.Code)
	}
	lower := strings.ToLower(resp.Error.Message)
	for _, forbidden := range []string{"openrouter", "insufficient", "credit", "balance", "402", "payment", "settings/credits"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("upstream funding/identity vocabulary reached the customer: %q in %q", forbidden, resp.Error.Message)
		}
	}
}

// TestExecuteSync_UpstreamPaymentRequired_ReleasesHoldAndDoesNotBill is the
// sync /v1/chat/completions and /v1/responses regression guard.
func TestExecuteSync_UpstreamPaymentRequired_ReleasesHoldAndDoesNotBill(t *testing.T) {
	hits := 0
	litellmSrv := paymentRequiredServer(&hits)
	defer litellmSrv.Close()
	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	w := callSyncCtx(orch, context.Background())

	assertUpstreamUnavailableBody(t, w)

	if hits != 1 {
		t.Fatalf("litellm hits = %d, want 1 (a 402 is neither 429 nor 5xx and must not be retried)", hits)
	}

	releaseBody, released := rec.find("/internal/accounting/reservations/release")
	if !released {
		t.Fatalf("expected the reservation to be released; calls seen: %+v", rec.calls)
	}
	if releaseBody["reason"] != "upstream_error" {
		t.Errorf("release reason = %v, want upstream_error", releaseBody["reason"])
	}
	if rec.has("/internal/accounting/reservations/finalize") {
		t.Fatalf("reservation must never be finalized (charged) on an upstream funding refusal; calls seen: %+v", rec.calls)
	}
}

// TestExecuteStreaming_UpstreamPaymentRequired_ReleasesHoldAndDoesNotBill is
// the streaming twin: the 402 arrives as LiteLLM's initial HTTP response,
// before the SSE 200 is ever committed to the client, so this is a
// pre-stream refusal, not a mid-stream error frame.
func TestExecuteStreaming_UpstreamPaymentRequired_ReleasesHoldAndDoesNotBill(t *testing.T) {
	hits := 0
	litellmSrv := paymentRequiredServer(&hits)
	defer litellmSrv.Close()
	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, []byte("{}"), "gpt-4o", "gpt-4o",
		NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, 10000, false, nil, orch.litellm.ChatCompletion)

	assertUpstreamUnavailableBody(t, w)

	if hits != 1 {
		t.Fatalf("litellm hits = %d, want 1 (a 402 is neither 429 nor 5xx and must not be retried)", hits)
	}
	releaseBody, released := rec.find("/internal/accounting/reservations/release")
	if !released {
		t.Fatalf("expected the reservation to be released; calls seen: %+v", rec.calls)
	}
	if releaseBody["reason"] != "upstream_error" {
		t.Errorf("release reason = %v, want upstream_error", releaseBody["reason"])
	}
	if rec.has("/internal/accounting/reservations/finalize") {
		t.Fatalf("reservation must never be finalized (charged) on an upstream funding refusal; calls seen: %+v", rec.calls)
	}
}
