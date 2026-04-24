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
