package rag

// Zero-content guard on the UPSTREAM_ACTUAL pricing arm of both halves of
// POST /v1/rag/chat (issue #1538).
//
// settleChat branches on the pricing mode, and the guard shipped for #1526
// lives inside inference.ChatSettlementCredits, which only the catalog-priced
// arm reaches. The other arm calls inference.UpstreamActualSettlement, which
// reports Delivered on any successful cost read, so a reasoning burn on
// hive-auto (the only upstream_actual alias in the live catalog) was charged
// the cost the upstream reported for tokens the customer never saw.
//
// The invariant, on both arms: a turn is charged if and only if it delivered
// assistant-visible text, a tool call or a refusal. Everything else releases
// its hold under reason zero_content and is charged nothing.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
)

// The burn shapes of the sibling file, carrying the upstream's own reported
// cost. The cost is what separates this arm from the catalog-priced one: the
// read succeeds, so the settlement comes back delivered and confirmed, and the
// blank answer was billed at the reported cost rather than at nothing.
const (
	variablePriceBurnBody = `{"id":"gen-burn","choices":[{"message":{"role":"assistant","content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":111,"completion_tokens":700,"total_tokens":811,"cost":0.0004,"completion_tokens_details":{"reasoning_tokens":700}}}`

	variablePriceBurnUsageFrame = `{"id":"gen-burn","choices":[],"usage":{"prompt_tokens":111,"completion_tokens":700,"total_tokens":811,"cost":0.0004,"completion_tokens_details":{"reasoning_tokens":700}}}`
)

// A completed turn on a variable-price alias that carried no assistant-visible
// text is not charged, on either half, exactly as the same turn on a
// catalog-priced alias is not. The absorbed credits are the upstream's own
// reported cost, and they land on the surface that produced them.
//
// wantUpstreamActualCredits (560,000) is the same figure the delivered-turn
// test in billing_test.go pins, which is what makes the before-and-after
// comparable: the guard changes whether that charge happens, never its size.
func TestRAGChatDoesNotBillAVariablePriceReasoningBurn(t *testing.T) {
	tests := []struct {
		name    string
		surface string
		other   string
		stream  bool
		body    string
	}{
		{
			name: "non streaming", surface: inference.ZeroContentSurfaceRAGSync,
			other: inference.ZeroContentSurfaceRAGStream, stream: false,
			body: variablePriceBurnBody,
		},
		{
			name: "streaming", surface: inference.ZeroContentSurfaceRAGStream,
			other: inference.ZeroContentSurfaceRAGSync, stream: true,
			body: sseBody(burnOpenFrame, burnFinishFrame, variablePriceBurnUsageFrame, "[DONE]"),
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
				upstreamActualSelectRoute("route-test"),
				fakeDispatch(http.StatusOK, tc.body, nil))

			w := httptest.NewRecorder()
			h.handleChat(w, chatReq(t, ChatRequest{
				Model:    "hive-auto",
				Stream:   tc.stream,
				Messages: []ChatMessage{{Role: "user", Content: "what is the answer"}},
			}, uuid.New()))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
			}

			reservations, finalized, released := acct.counts()
			if reservations != 1 {
				t.Fatalf("reservations = %d, want 1", reservations)
			}
			if finalized != 0 {
				t.Errorf("a variable-price turn carrying no readable text must not be charged the reported cost: finalized = %d", finalized)
			}
			if released != 1 {
				t.Fatalf("the hold must be handed back exactly once: released = %d", released)
			}
			if reason := releases(acct)[0].Reason; reason != "zero_content" {
				t.Errorf("release reason = %q, want %q", reason, "zero_content")
			}

			after, _ := absorbedCredits(t, reg, tc.surface)
			if after-before != float64(wantUpstreamActualCredits) {
				t.Errorf("absorbed credits on %q = %v, want %v (the upstream's own reported cost)",
					tc.surface, after-before, wantUpstreamActualCredits)
			}
			otherAfter, _ := absorbedCredits(t, reg, tc.other)
			if otherAfter != otherBefore {
				t.Errorf("a burn on %q was attributed to %q", tc.surface, tc.other)
			}
		})
	}
}
