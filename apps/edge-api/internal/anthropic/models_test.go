package anthropic_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/anthropic"
)

// openAIModelListBody is the exact body edge-api's own GET /v1/models writes.
const openAIModelListBody = `{"object":"list","data":[` +
	`{"id":"hive-default","object":"model","created":1716935002,"owned_by":"hive","name":"Hive Default"},` +
	`{"id":"hive-fast","object":"model","created":1716935003,"owned_by":"hive"}` +
	`]}`

func openAIModelsHandler(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

// TestModelsCompat_AnthropicClientGetsTheAnthropicListShape is the issue #1259
// body-shape guard. Verified against https://docs.claude.com/en/api/models-list
// on 2026-08-28: each entry is {"type":"model","id":...,"display_name":...,
// "created_at":"<RFC 3339>"} and the envelope carries has_more plus the
// nullable first_id / last_id cursors, none of which the OpenAI shape has.
func TestModelsCompat_AnthropicClientGetsTheAnthropicListShape(t *testing.T) {
	h := anthropic.ModelsCompat(openAIModelsHandler(http.StatusOK, openAIModelListBody))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("x-api-key", "hk_live_test")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	var got anthropic.ModelsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if len(got.Data) != 2 {
		t.Fatalf("data: want 2 entries got %d (%s)", len(got.Data), rec.Body.String())
	}
	if got.Data[0].Type != "model" {
		t.Errorf("data[0].type: want model got %q", got.Data[0].Type)
	}
	if got.Data[0].ID != "hive-default" {
		t.Errorf("data[0].id: want hive-default got %q", got.Data[0].ID)
	}
	if got.Data[0].DisplayName != "Hive Default" {
		t.Errorf("data[0].display_name: want Hive Default got %q", got.Data[0].DisplayName)
	}
	// An entry with no name falls back to its id rather than an empty string:
	// display_name is a required field a client renders directly.
	if got.Data[1].DisplayName != "hive-fast" {
		t.Errorf("data[1].display_name: want the id as fallback got %q", got.Data[1].DisplayName)
	}
	if _, err := time.Parse(time.RFC3339, got.Data[0].CreatedAt); err != nil {
		t.Errorf("data[0].created_at %q is not RFC 3339: %v", got.Data[0].CreatedAt, err)
	}
	if got.HasMore {
		t.Error("has_more: this route serves the whole entitled list, so it must be false")
	}
	if got.FirstID == nil || *got.FirstID != "hive-default" {
		t.Errorf("first_id: want hive-default got %v", got.FirstID)
	}
	if got.LastID == nil || *got.LastID != "hive-fast" {
		t.Errorf("last_id: want hive-fast got %v", got.LastID)
	}

	// The OpenAI-only keys must be gone, not merely supplemented: a real SDK
	// parses this into a typed page and an "object":"list" envelope has no
	// data array it can read.
	raw := rec.Body.String()
	for _, absent := range []string{`"object"`, `"owned_by"`} {
		if strings.Contains(raw, absent) {
			t.Errorf("Anthropic list body still carries the OpenAI key %s: %s", absent, raw)
		}
	}
}

// TestModelsCompat_EmptyListStillCarriesNullCursors pins the shape of a page
// with nothing on it: data is an array (never null, same defect class as issue
// #1260) and both cursors are explicitly null rather than absent.
func TestModelsCompat_EmptyListStillCarriesNullCursors(t *testing.T) {
	h := anthropic.ModelsCompat(openAIModelsHandler(http.StatusOK, `{"object":"list","data":[]}`))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("anthropic-version", "2023-06-01")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	raw := rec.Body.String()
	if !strings.Contains(raw, `"data":[]`) {
		t.Errorf("empty list must serialize data as []: %s", raw)
	}
	if !strings.Contains(raw, `"first_id":null`) || !strings.Contains(raw, `"last_id":null`) {
		t.Errorf("empty list must carry explicit null cursors: %s", raw)
	}
}

// TestModelsCompat_ErrorUsesTheAnthropicEnvelope is the issue #1259 error-shape
// guard. anthropic.APIStatusError reads its .type from body["error"]["type"],
// and the OpenAI envelope has no top-level "type":"error" and carries a value
// there that is not a member of Anthropic's error enum, so a client inspecting
// either field got nothing usable.
func TestModelsCompat_ErrorUsesTheAnthropicEnvelope(t *testing.T) {
	openAIRefusal := `{"error":{"message":"You didn't provide an API key.","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`
	h := anthropic.ModelsCompat(openAIModelsHandler(http.StatusUnauthorized, openAIRefusal))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("x-api-key", "hk_bad")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: want 401 got %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["type"] != "error" {
		t.Errorf("envelope: want top-level type=error got %v (%s)", body["type"], rec.Body.String())
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["type"] != "authentication_error" {
		t.Errorf("error.type: want authentication_error got %v", errObj["type"])
	}
	if errObj["message"] != "You didn't provide an API key." {
		t.Errorf("error.message: want the underlying refusal preserved got %v", errObj["message"])
	}
}

// TestModelsCompat_OpenAIClientIsUntouched is the non-regression half. Open
// WebUI's model picker is built from this route and parses the OpenAI shape;
// re-shaping unconditionally would empty it.
func TestModelsCompat_OpenAIClientIsUntouched(t *testing.T) {
	h := anthropic.ModelsCompat(openAIModelsHandler(http.StatusOK, openAIModelListBody))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer hk_live_test")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Body.String() != openAIModelListBody {
		t.Fatalf("an OpenAI-shaped caller must get the byte-identical OpenAI body\nwant: %s\ngot:  %s",
			openAIModelListBody, rec.Body.String())
	}
}

func TestIsAnthropicClient(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{"x-api-key, the SDK default credential", map[string]string{"x-api-key": "hk_1"}, true},
		{"anthropic-version, sent by every official SDK", map[string]string{"anthropic-version": "2023-06-01"}, true},
		{"bearer only, an OpenAI-shaped caller", map[string]string{"Authorization": "Bearer hk_1"}, false},
		{"no credential at all", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			if got := anthropic.IsAnthropicClient(req); got != tc.want {
				t.Errorf("IsAnthropicClient = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestModelsCompat_RefusalCarriesTheRetryHeaders is the regression guard for
// the header half of a refusal. handleModels answers an unauthorized or
// throttled API-key caller through apierrors.WriteAuthFailure, which delivers
// the status and message in the body and the retry metadata in the headers.
// Buffering the body for reshaping and dropping the headers would leave an
// Anthropic SDK backing off on its own default schedule against a gateway that
// just told it exactly how long to wait.
func TestModelsCompat_RefusalCarriesTheRetryHeaders(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "30")
		w.Header().Set("X-Ratelimit-Limit-Requests", "100")
		w.Header().Set("X-Ratelimit-Remaining-Requests", "0")
		// A stale length for the OpenAI-shaped body, which must not follow the
		// shorter Anthropic envelope onto the wire.
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Rate limit reached.","type":"rate_limit_error","code":"rate_limit_exceeded"}}`))
	})

	h := anthropic.ModelsCompat(upstream)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("anthropic-version", "2023-06-01")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status: want 429 got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "30" {
		t.Errorf("retry-after: want 30 got %q", got)
	}
	if got := rec.Header().Get("X-Ratelimit-Limit-Requests"); got != "100" {
		t.Errorf("x-ratelimit-limit-requests: want 100 got %q", got)
	}
	if got := rec.Header().Get("X-Ratelimit-Remaining-Requests"); got != "0" {
		t.Errorf("x-ratelimit-remaining-requests: want 0 got %q", got)
	}
	if got := rec.Header().Get("Content-Length"); got == "4096" {
		t.Errorf("content-length: the delegated body length must not describe the reshaped one, got %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type: want application/json got %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if body["type"] != "error" {
		t.Errorf("envelope: want top-level type=error got %v", body["type"])
	}
}

// TestModelsCompat_DeclaresVary pins the cache-correctness half of serving two
// representations from one URL. Nothing on this route sets Cache-Control and
// the route needs a credential, so no correct cache stores it today; the
// declaration is what keeps a future edge cache, or an intermediary keying on
// URL alone, from handing an Anthropic-shaped body to Open WebUI and emptying
// its model picker.
func TestModelsCompat_DeclaresVary(t *testing.T) {
	h := anthropic.ModelsCompat(openAIModelsHandler(http.StatusOK, openAIModelListBody))

	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{name: "anthropic client", headers: map[string]string{"x-api-key": "hk_live_test"}},
		{name: "openai client", headers: map[string]string{"Authorization": "Bearer hk_live_test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			got := rec.Header().Get("Vary")
			if !strings.Contains(got, "anthropic-version") || !strings.Contains(got, "x-api-key") {
				t.Errorf("vary: want both request headers that select the representation, got %q", got)
			}
		})
	}
}
