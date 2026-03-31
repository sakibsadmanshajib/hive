package errors

import (
	"encoding/json"
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

func decodeOpenAIError(t *testing.T, w *httptest.ResponseRecorder) OpenAIError {
	t.Helper()

	var resp OpenAIError
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	return resp
}

func assertNoProviderLeak(t *testing.T, message string) {
	t.Helper()

	for _, forbidden := range []string{"openrouter", "groq", "route-openrouter-default", "openrouter/auto"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("expected provider-blind message, found %q in %q", forbidden, message)
		}
	}
}
