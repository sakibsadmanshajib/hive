package errors

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteProviderBlindUpstreamErrorStripsProviderStrings(t *testing.T) {
	w := httptest.NewRecorder()

	WriteProviderBlindUpstreamError(w, "hive-fast", http.StatusBadGateway, "openrouter route-openrouter-default failed after openrouter/auto retried groq and route-groq-fast")

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}

	resp := decodeOpenAIError(t, w)
	if resp.Error.Type != "api_error" {
		t.Fatalf("type = %q, want %q", resp.Error.Type, "api_error")
	}
	if resp.Error.Code == nil || *resp.Error.Code != "upstream_error" {
		t.Fatalf("code = %v, want %q", resp.Error.Code, "upstream_error")
	}
	if !strings.Contains(resp.Error.Message, "hive-fast") {
		t.Fatalf("expected alias in sanitized message, got %q", resp.Error.Message)
	}
	assertNoProviderLeak(t, resp.Error.Message)
}

func TestWriteProviderBlindUpstreamErrorMaps429ToRateLimit(t *testing.T) {
	w := httptest.NewRecorder()

	WriteProviderBlindUpstreamError(w, "hive-fast", http.StatusTooManyRequests, "route-groq-fast hit groq rate limits")

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}

	resp := decodeOpenAIError(t, w)
	if resp.Error.Type != "rate_limit_error" {
		t.Fatalf("type = %q, want %q", resp.Error.Type, "rate_limit_error")
	}
	if resp.Error.Code == nil || *resp.Error.Code != "upstream_rate_limited" {
		t.Fatalf("code = %v, want %q", resp.Error.Code, "upstream_rate_limited")
	}
	assertNoProviderLeak(t, resp.Error.Message)
}

func TestWriteProviderBlindUpstreamErrorMaps503ToUnavailable(t *testing.T) {
	w := httptest.NewRecorder()

	WriteProviderBlindUpstreamError(w, "", http.StatusServiceUnavailable, "openrouter/auto is unavailable via route-openrouter-default")

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	resp := decodeOpenAIError(t, w)
	if resp.Error.Type != "api_error" {
		t.Fatalf("type = %q, want %q", resp.Error.Type, "api_error")
	}
	if resp.Error.Code == nil || *resp.Error.Code != "upstream_unavailable" {
		t.Fatalf("code = %v, want %q", resp.Error.Code, "upstream_unavailable")
	}
	assertNoProviderLeak(t, resp.Error.Message)
}

func TestWriteProviderBlindUpstreamErrorSanitizesNestedJSONAndLogsRawDetails(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("x-request-id", "req-test-123")

	raw := `{"error":{"message":"litellm.AuthenticationError: AuthenticationError: OpenrouterException: route-openrouter-default rejected openrouter/openrouter/free","type":"auth_error"}}`

	logOutput := captureProviderBlindLogs(t, func() {
		WriteProviderBlindUpstreamError(w, "hive-fast", http.StatusUnauthorized, raw)
	})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	resp := decodeOpenAIError(t, w)
	if !strings.Contains(resp.Error.Message, "hive-fast") {
		t.Fatalf("expected alias in sanitized message, got %q", resp.Error.Message)
	}
	assertNoProviderLeak(t, resp.Error.Message)

	if !strings.Contains(logOutput, `request_id="req-test-123"`) {
		t.Fatalf("expected request id in internal log, got %q", logOutput)
	}
	for _, expected := range []string{"litellm.AuthenticationError", "OpenrouterException", "route-openrouter-default"} {
		if !strings.Contains(logOutput, expected) {
			t.Fatalf("expected raw upstream details %q in internal log, got %q", expected, logOutput)
		}
	}
}

func TestWriteProviderBlindUpstreamErrorFallsBackWhenAliasContainsProviderName(t *testing.T) {
	w := httptest.NewRecorder()

	WriteProviderBlindUpstreamError(w, "openrouter/openrouter/free", http.StatusBadGateway, "OpenrouterException: openrouter/openrouter/free failed via route-openrouter-default")

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}

	resp := decodeOpenAIError(t, w)
	if !strings.Contains(strings.ToLower(resp.Error.Message), "requested model") {
		t.Fatalf("expected generic resource label in sanitized message, got %q", resp.Error.Message)
	}
	assertNoProviderLeak(t, resp.Error.Message)
}

func TestWriteProviderBlindUpstreamErrorHidesInternalTransportDetails(t *testing.T) {
	w := httptest.NewRecorder()

	WriteProviderBlindUpstreamError(
		w,
		"hive-default",
		http.StatusBadGateway,
		`litellm: request failed: Post "http://litellm:4000/chat/completions": dial tcp 172.19.0.3:4000: connect: connection refused`,
	)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}

	resp := decodeOpenAIError(t, w)
	if resp.Error.Message != "hive-default is temporarily unavailable." {
		t.Fatalf("message = %q, want %q", resp.Error.Message, "hive-default is temporarily unavailable.")
	}
	assertNoProviderLeak(t, resp.Error.Message)
}

func TestWriteProviderBlindUpstreamErrorHidesFallbackRoutingInternals(t *testing.T) {
	w := httptest.NewRecorder()

	// Reproduces issue #965: a model_not_found upstream response wrapped in
	// LiteLLM's own fallback-group bookkeeping. No literal provider name
	// appears in "No fallback model group found ... Fallbacks=[...] ...
	// Retried: 3 times", so the provider-name regex alone never catches it;
	// the message must still not reach the customer.
	raw := `upstream error - {"error":{"message":"The model ` + "`llama-3.1-8b-instant`" + ` does not exist or you do not have access to it.","type":"invalid_request_error","code":"model_not_found"}} No fallback model group found for original model_group=hive-fast. Fallbacks=[{'hive-fast': ['hive-fast']}, {'hive-fast': ['hive-fast']}, {'hive-fast': ['hive-fast']}]. Upstream provider Retried: 3 times`

	WriteProviderBlindUpstreamError(w, "hive-fast", http.StatusNotFound, raw)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	resp := decodeOpenAIError(t, w)
	assertNoProviderLeak(t, resp.Error.Message)
	for _, forbidden := range []string{
		"fallback model group",
		"model_group=",
		"fallbacks=",
		"retried:",
		"llama-3.1-8b-instant",
	} {
		if strings.Contains(strings.ToLower(resp.Error.Message), forbidden) {
			t.Fatalf("expected routing internals stripped, found %q in %q", forbidden, resp.Error.Message)
		}
	}
	if resp.Error.Message != "hive-fast is not available." {
		t.Fatalf("message = %q, want %q", resp.Error.Message, "hive-fast is not available.")
	}
}

func TestProviderBlindLooksLikeRoutingInternalsRequiresSpecificToken(t *testing.T) {
	w := httptest.NewRecorder()

	// "retried:" alone, with none of LiteLLM's specific bookkeeping tokens,
	// must NOT trigger the routing-internals collapse: that rule is specific
	// to LiteLLM's own vocabulary and over-firing it would mislabel unrelated
	// failures (security review finding on the PR that added it).
	//
	// The assertion changed with PR #1303 and the rule it names did not. That
	// PR inverted the tail of sanitizeProviderBlindMessage to an allowlist, so
	// an unrecognized upstream sentence is no longer forwarded verbatim; it
	// collapses to the status fallback. The two outcomes stay distinguishable,
	// which is what keeps this test able to go red: the routing-internals rule
	// answers "is not available." and the fallback answers "request failed."
	//
	// The earlier verbatim assertion rested on a security review's judgement
	// that collapsing unmatched text threw away real detail for no safety
	// gain. Measurement overtook that: with forward-by-default, five separate
	// upstream billing shapes reached customers word for word (see
	// provider_blind_allowlist_test.go). The detail lost here is "retried: 3
	// times before giving up", which is our retry bookkeeping and not
	// something the caller can act on.
	WriteProviderBlindUpstreamError(w, "hive-default", http.StatusBadGateway, "request failed, retried: 3 times before giving up")

	resp := decodeOpenAIError(t, w)
	if resp.Error.Message == "hive-default is not available." {
		t.Fatalf("routing-internals rule fired on a message with none of its tokens: %q", resp.Error.Message)
	}
	if resp.Error.Message != "hive-default request failed." {
		t.Fatalf("expected the status fallback, got %q", resp.Error.Message)
	}
	if strings.Contains(resp.Error.Message, "retried") {
		t.Fatalf("retry bookkeeping reached the customer: %q", resp.Error.Message)
	}
}

func decodeOpenAIError(t *testing.T, w *httptest.ResponseRecorder) OpenAIError {
	t.Helper()

	var resp OpenAIError
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	return resp
}

func captureProviderBlindLogs(t *testing.T, fn func()) string {
	t.Helper()

	var buf bytes.Buffer
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	originalPrefix := log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	defer log.SetOutput(originalWriter)
	defer log.SetFlags(originalFlags)
	defer log.SetPrefix(originalPrefix)

	fn()
	return buf.String()
}

func assertNoProviderLeak(t *testing.T, message string) {
	t.Helper()

	lowerMessage := strings.ToLower(message)
	for _, forbidden := range []string{
		"openrouter",
		"groq",
		"litellm",
		"route-openrouter-default",
		"route-groq-fast",
		"openrouter/auto",
		"openrouterexception",
		"authenticationerror",
	} {
		if strings.Contains(lowerMessage, forbidden) {
			t.Fatalf("expected provider-blind message, found %q in %q", forbidden, message)
		}
	}
}
