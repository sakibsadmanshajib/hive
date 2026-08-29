package errors

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Issue #1348: an invalid message role and an oversized max_tokens both came
// back as 400 with {"message":"hive-small is not available.","code":
// "upstream_error"}. Both are malformed requests. The sentence sent a
// customer debugging their own payload to go and check model status, and
// upstream_error is the wrong code for a refusal of the request itself.
//
// The bookkeeping fixture below is LiteLLM own, measured live: an upstream
// 400 exhausts the fallback group, and the group summary is what reaches this
// function.
const routingBookkeeping400 = `{"error":{"message":"litellm.BadRequestError: OpenrouterException - Invalid message role"}} No fallback model group found for original model_group=hive-small. Fallbacks=[{"hive-small": ["hive-small"]}]. Retried: 3 times`

func TestUpstreamRequestRefusalIsNotAnAvailabilityVerdict(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity} {
		w := httptest.NewRecorder()
		WriteProviderBlindUpstreamError(w, "hive-small", status, routingBookkeeping400)

		if w.Code != status {
			t.Fatalf("status = %d, want %d", w.Code, status)
		}
		raw := w.Body.String()
		// Serialized bytes, not the struct: the envelope fields are what an
		// SDK branches on and what a human reads.
		if !strings.Contains(raw, `"code":"invalid_request"`) {
			t.Errorf("wire body does not carry the request-shaped code: %s", raw)
		}
		if !strings.Contains(raw, `"type":"invalid_request_error"`) {
			t.Errorf("wire body does not carry the request-shaped type: %s", raw)
		}
		if strings.Contains(raw, "upstream_error") {
			t.Errorf("a request the gateway refused is still labelled an upstream failure: %s", raw)
		}
		if strings.Contains(raw, "is not available") {
			t.Errorf("a malformed request is still answered with an availability verdict: %s", raw)
		}
		resp := decodeOpenAIError(t, w)
		if !strings.Contains(resp.Error.Message, "hive-small") {
			t.Errorf("message no longer names the alias the caller asked for: %q", resp.Error.Message)
		}
		assertNoProviderLeak(t, resp.Error.Message)
		for _, forbidden := range []string{"fallback model group", "model_group=", "Fallbacks=", "Retried:", "BadRequestError"} {
			if strings.Contains(resp.Error.Message, forbidden) {
				t.Errorf("routing internals reached the customer: %q in %q", forbidden, resp.Error.Message)
			}
		}
	}
}

// TestUpstreamAvailabilityVerdictSurvivesOnNonRequestStatuses is the other
// side of the gate, and the reason the change is status-gated rather than a
// blanket rewording. Deleting providerBlindRequestShaped and collapsing every
// status to the request-shaped sentence would pass the test above; it must
// turn this one red.
//
// A 404 from an upstream that does not know the model IS an availability
// answer, and a 502 or 503 is never the caller doing.
func TestUpstreamAvailabilityVerdictSurvivesOnNonRequestStatuses(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusNotFound, "hive-small is not available."},
		{http.StatusBadGateway, "hive-small is not available."},
		{http.StatusServiceUnavailable, "hive-small is not available."},
	} {
		w := httptest.NewRecorder()
		WriteProviderBlindUpstreamError(w, "hive-small", tc.status, routingBookkeeping400)

		resp := decodeOpenAIError(t, w)
		if resp.Error.Message != tc.want {
			t.Errorf("status %d: message = %q, want %q", tc.status, resp.Error.Message, tc.want)
		}
		if strings.Contains(w.Body.String(), `"type":"invalid_request_error"`) {
			t.Errorf("status %d: an availability failure is labelled a caller mistake: %s", tc.status, w.Body.String())
		}
	}
}
