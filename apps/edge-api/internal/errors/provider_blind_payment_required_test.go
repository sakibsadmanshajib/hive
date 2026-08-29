package errors

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWriteProviderBlindUpstreamErrorRemaps402ToUpstreamUnavailable is the
// regression guard for issue #1411: OpenRouter (or any upstream) answering
// 402 Payment Required is talking about ITS OWN account balance with the
// upstream vendor, never the Hive customer's balance: CreateReservation
// already verified the customer's own credit before dispatch reached this
// point. Forwarding a literal 402 to the customer says the opposite: it
// reads as "you must pay," which is backwards and is exactly the "provider
// refusal presented as a customer/model fault" shape the issue names.
//
// The fixture below is OpenRouter's real documented error envelope
// (https://openrouter.ai/docs/api_reference/errors-and-debugging.md,
// confirmed live 2026-08-29): {"error":{"code":402,"message":"...",
// "metadata":{"error_type":"payment_required"}}}, HTTP status equal to
// error.code. This is a SIMULATED upstream response (an in-process
// httptest.ResponseRecorder, no network call to any real provider); the
// shape is the part that is real, not the transport.
func TestWriteProviderBlindUpstreamErrorRemaps402ToUpstreamUnavailable(t *testing.T) {
	w := httptest.NewRecorder()

	raw := `{"error":{"code":402,"message":"Insufficient credits. Add more using https://openrouter.ai/settings/credits","metadata":{"error_type":"payment_required"}}}`
	logs := captureProviderBlindLogs(t, func() {
		WriteProviderBlindUpstreamError(w, "hive-auto", http.StatusPaymentRequired, raw)
	})

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (a provider funding refusal must not read as the CUSTOMER owing money)", w.Code, http.StatusServiceUnavailable)
	}

	resp := decodeOpenAIError(t, w)
	if resp.Error.Code == nil || *resp.Error.Code != "upstream_unavailable" {
		t.Fatalf("code = %v, want %q", resp.Error.Code, "upstream_unavailable")
	}
	if resp.Error.Type != "api_error" {
		t.Fatalf("type = %q, want %q", resp.Error.Type, "api_error")
	}
	if resp.Error.Message != "hive-auto is temporarily unavailable." {
		t.Fatalf("message = %q, want the same verdict a 503/504 already gets", resp.Error.Message)
	}
	assertNoProviderLeak(t, resp.Error.Message)
	for _, forbidden := range []string{"insufficient", "credit", "balance", "402", "payment"} {
		if strings.Contains(strings.ToLower(resp.Error.Message), forbidden) {
			t.Fatalf("upstream funding vocabulary reached the customer: %q in %q", forbidden, resp.Error.Message)
		}
	}

	// The operator log must keep the REAL status (402) and the raw body, even
	// though the customer-facing status changed to 503: this is a diagnosis
	// aid, not a second leak surface.
	if !strings.Contains(logs, "status=402") {
		t.Fatalf("operator log lost the real upstream status: %q", logs)
	}
	if !strings.Contains(logs, "payment_required") {
		t.Fatalf("operator log lost the raw upstream body: %q", logs)
	}
}
