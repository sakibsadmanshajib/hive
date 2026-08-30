package rag

// Zero-content guard on both halves of POST /v1/rag/chat (issue #1526).
//
// A reasoning burn spends the caller's whole completion ceiling on hidden
// reasoning and returns nothing the customer can read. PR #1499 stopped it
// being billed on the API-key streaming path; this endpoint settles through
// inference.ChatSettlementCredits instead, on both its streaming and its
// non-streaming half, so both kept charging full catalog price for a blank
// answer.
//
// Each case asserts the terminal state of the hold and the reason recorded for
// a release, never the status code: a burn releases as "zero_content", a served
// answer finalizes, and the two must not be confusable in the ledger.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
)

// The reasoning-burn shape on each half of this endpoint. Both carry a
// confident usage block: the burn is real inference that Hive paid for, which
// is exactly why it used to settle as an ordinary success.
const (
	burnBody = `{"id":"up-1","choices":[{"message":{"role":"assistant","content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":111,"completion_tokens":700,"total_tokens":811,"completion_tokens_details":{"reasoning_tokens":700}}}`

	burnOpenFrame   = `{"id":"up-1","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`
	burnFinishFrame = `{"id":"up-1","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`
	burnUsageFrame  = `{"id":"up-1","choices":[],"usage":{"prompt_tokens":111,"completion_tokens":700,"total_tokens":811,"completion_tokens_details":{"reasoning_tokens":700}}}`
)

// releases returns every release recorded so far, for asserting the REASON and
// not merely the count. A burn filed as an upstream fault or as a customer
// hanging up loses the absorbed cost in the ledger.
func releases(f *ragAccounting) []inference.ReleaseReservationInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]inference.ReleaseReservationInput(nil), f.released...)
}

func burnChatRequest(t *testing.T, stream bool) *http.Request {
	t.Helper()
	return chatReq(t, ChatRequest{
		Model:    "hive-free",
		Messages: []ChatMessage{{Role: "user", Content: "what is the answer"}},
		Stream:   stream,
	}, uuid.New())
}

// Non-streaming: a fully present response body whose every choice finished at
// the ceiling with no content is a burn. Nothing about a stream is consulted --
// the body is whole by construction, so emptiness is a property of the content.
func TestRAGChatDoesNotBillANonStreamingReasoningBurn(t *testing.T) {
	acct := &ragAccounting{}
	h := newBilledChatHandler(t, acct, billableTenant(),
		fakeSelectRoute("route-test", nil), fakeDispatch(http.StatusOK, burnBody, nil))

	w := httptest.NewRecorder()
	h.handleChat(w, burnChatRequest(t, false))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	reservations, finalized, released := acct.counts()
	if reservations != 1 {
		t.Fatalf("reservations = %d, want 1", reservations)
	}
	if finalized != 0 {
		t.Errorf("a response carrying no readable text must not be charged: finalized = %d", finalized)
	}
	if released != 1 {
		t.Fatalf("the hold must be handed back exactly once: released = %d", released)
	}
	if reason := releases(acct)[0].Reason; reason != "zero_content" {
		t.Errorf("release reason = %q, want %q", reason, "zero_content")
	}
}

// Streaming: same shape, arriving as the LiteLLM frame sequence (a finish frame
// followed by a usage-only terminal frame) and ending at the upstream's own
// [DONE].
func TestRAGChatDoesNotBillAStreamingReasoningBurn(t *testing.T) {
	acct := &ragAccounting{}
	h := newBilledChatHandler(t, acct, billableTenant(),
		fakeSelectRoute("route-test", nil),
		fakeDispatch(http.StatusOK, sseBody(burnOpenFrame, burnFinishFrame, burnUsageFrame, "[DONE]"), nil))

	w := httptest.NewRecorder()
	h.handleChat(w, burnChatRequest(t, true))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	reservations, finalized, released := acct.counts()
	if reservations != 1 {
		t.Fatalf("reservations = %d, want 1", reservations)
	}
	if finalized != 0 {
		t.Errorf("a stream carrying no readable text must not be charged: finalized = %d", finalized)
	}
	if released != 1 {
		t.Fatalf("the hold must be handed back exactly once: released = %d", released)
	}
	if reason := releases(acct)[0].Reason; reason != "zero_content" {
		t.Errorf("release reason = %q, want %q", reason, "zero_content")
	}
}

// The control for both guards above: the same frames with one visible token in
// them still bill, on both halves. Without these, a guard that released every
// hold would pass.
func TestRAGChatStillBillsADeliveredAnswer(t *testing.T) {
	tests := []struct {
		name   string
		stream bool
		body   string
	}{
		{"non streaming", false, canned200Response},
		{"streaming", true, sseBody(
			`{"id":"up-1","choices":[{"index":0,"delta":{"content":"42"},"finish_reason":null}]}`,
			burnFinishFrame, burnUsageFrame, "[DONE]")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			acct := &ragAccounting{}
			h := newBilledChatHandler(t, acct, billableTenant(),
				fakeSelectRoute("route-test", nil), fakeDispatch(http.StatusOK, tc.body, nil))

			w := httptest.NewRecorder()
			h.handleChat(w, burnChatRequest(t, tc.stream))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
			}
			_, finalized, released := acct.counts()
			if finalized != 1 || released != 0 {
				t.Errorf("a delivered answer is charged: finalized = %d released = %d", finalized, released)
			}
		})
	}
}

// A stream with no visible text that never reached its upstream's own end is
// not a burn: the frames that never arrived are the ones whose contents are
// unknown, and one of them could have carried the entire answer. It bills
// (D-034, fail closed).
func TestRAGChatBillsAnEmptyStreamThatNeverCompleted(t *testing.T) {
	acct := &ragAccounting{}
	h := newBilledChatHandler(t, acct, billableTenant(),
		fakeSelectRoute("route-test", nil),
		fakeDispatch(http.StatusOK, sseBody(burnOpenFrame, burnFinishFrame, burnUsageFrame), nil))

	w := httptest.NewRecorder()
	h.handleChat(w, burnChatRequest(t, true))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	_, finalized, released := acct.counts()
	if finalized != 1 || released != 0 {
		t.Errorf("an incomplete stream bills: finalized = %d released = %d", finalized, released)
	}
}

// absorbedCredits reads one surface's series off a registry, reporting
// separately whether the series exists at all. Absent is a distinct failure
// from zero: a CounterVec emits nothing until its first increment, so a missing
// series and a quiet day are byte-identical on a dashboard unless the series is
// created at registration.
func absorbedCredits(t *testing.T, reg *prometheus.Registry, surface string) (float64, bool) {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "hive_chat_zero_content_absorbed_credits_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "surface" && label.GetValue() == surface {
					return metric.GetCounter().GetValue(), true
				}
			}
		}
	}
	return 0, false
}

// The counter's only job here is to be provable: it must be able to fire in the
// population it claims to cover, on BOTH halves of this endpoint. The failure
// this guards against is a counter keyed on a quantity one path never has, so
// the dashboard reads clean forever while the loss continues: exactly what
// hive_usage_reasoning_tokens_unbilled_total did on this very endpoint, whose
// handler dropped completion_tokens_details before the counter's branch could
// be reached (#1472).
//
// Each case asserts the series exists before anything runs, that it moves on a
// burn through its own half, and that the other half's series does not, which
// is what proves the surface label is wired to the surface it names.
func TestRAGChatZeroContentCounterCanFire(t *testing.T) {
	tests := []struct {
		name    string
		surface string
		other   string
		stream  bool
		body    string
	}{
		{
			name: "non streaming", surface: inference.ZeroContentSurfaceRAGSync,
			other: inference.ZeroContentSurfaceRAGStream, stream: false, body: burnBody,
		},
		{
			name: "streaming", surface: inference.ZeroContentSurfaceRAGStream,
			other: inference.ZeroContentSurfaceRAGSync, stream: true,
			body: sseBody(burnOpenFrame, burnFinishFrame, burnUsageFrame, "[DONE]"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			inference.RegisterZeroContentMetrics(reg)
			before, found := absorbedCredits(t, reg, tc.surface)
			if !found {
				t.Fatalf("series for surface %q must exist from registration, so zero reads as zero", tc.surface)
			}
			otherBefore, otherFound := absorbedCredits(t, reg, tc.other)
			if !otherFound {
				t.Fatalf("series for surface %q must exist from registration", tc.other)
			}

			acct := &ragAccounting{}
			h := newBilledChatHandler(t, acct, billableTenant(),
				fakeSelectRoute("route-test", nil), fakeDispatch(http.StatusOK, tc.body, nil))
			w := httptest.NewRecorder()
			h.handleChat(w, burnChatRequest(t, tc.stream))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
			}

			// 111 prompt and 700 completion tokens at the 300000/1200000 rate
			// fakeSelectRoute resolves to, recomputed here rather than read
			// from the production helper: 33300000 + 840000000 over a million,
			// rounded half up.
			const wantAbsorbed = float64(873)
			after, _ := absorbedCredits(t, reg, tc.surface)
			if after-before != wantAbsorbed {
				t.Errorf("absorbed credits on %q = %v, want %v", tc.surface, after-before, wantAbsorbed)
			}
			otherAfter, _ := absorbedCredits(t, reg, tc.other)
			if otherAfter != otherBefore {
				t.Errorf("a burn on %q was attributed to %q", tc.surface, tc.other)
			}
		})
	}
}
