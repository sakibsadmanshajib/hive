package images_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/images"
)

// Issue #1319: POST /v1/images/generations answered HTTP 200 with an empty
// data array. Every SDK reports that as success, so the caller code fails
// somewhere further away from the cause and a retry loop can never recover.
//
// Every assertion below reads the SERIALIZED response and the status code,
// never an internal struct: ImageData.URL and ImageData.B64JSON are omitempty
// pointers, so a struct-level assertion cannot tell an absent image from a
// present empty one, which is the exact blindness that let this ship.

// decodeImageErrorEnvelope pulls the OpenAI error envelope off the wire.
func decodeImageErrorEnvelope(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v (%s)", err, w.Body.String())
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected a top-level error object, got %s", w.Body.String())
	}
	return errObj
}

// assertImagelessRefusal is the shared verdict for both endpoints.
func assertImagelessRefusal(t *testing.T, w *httptest.ResponseRecorder, acc *mockAccounting, wantLabel string) {
	t.Helper()
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", w.Code, w.Body.String())
	}
	raw := w.Body.String()
	if strings.Contains(raw, "\"data\"") {
		t.Fatalf("an empty success envelope reached the wire: %s", raw)
	}
	errObj := decodeImageErrorEnvelope(t, w)
	if code, _ := errObj["code"].(string); code != "upstream_error" {
		t.Errorf("code = %q, want upstream_error", code)
	}
	if errType, _ := errObj["type"].(string); errType != "api_error" {
		t.Errorf("type = %q, want api_error", errType)
	}
	msg, _ := errObj["message"].(string)
	if msg == "" {
		t.Error("refusal carries no message for the caller to read")
	}
	if !strings.Contains(msg, wantLabel) {
		t.Errorf("message = %q, want it to name %q", msg, wantLabel)
	}
	for _, leak := range []string{"groq", "Groq", "openrouter", "OpenRouter", "openai", "OpenAI", "litellm", "LiteLLM"} {
		if strings.Contains(raw, leak) {
			t.Errorf("provider identity leaked into the refusal: %q in %s", leak, raw)
		}
	}
	if !acc.releaseCalled {
		t.Error("the hold was not released: the caller is charged for a request that returned no image")
	}
	if acc.finalizeCalled {
		t.Error("the hold was settled for a response carrying no image")
	}
}

// imagelessUpstreamBodies are the two shapes of a 2xx that carries no image.
// The second one is why the guard counts payloads rather than array entries: a
// length check alone would pass it straight through as a success.
var imagelessUpstreamBodies = []struct {
	name string
	body string
}{
	{"empty data array", `{"created":1700000000,"data":[]}`},
	{"data entry with no url and no b64_json", `{"created":1700000000,"data":[{"revised_prompt":"a red circle"}]}`},
	{"data entry with an empty url", `{"created":1700000000,"data":[{"url":""}]}`},
}

func TestImageGenerationRefusesResponseWithNoImage(t *testing.T) {
	for _, tc := range imagelessUpstreamBodies {
		t.Run(tc.name, func(t *testing.T) {
			mock := newMockLiteLLM([]byte(tc.body), 200, "application/json")
			defer mock.Close()

			acc := &mockAccounting{reservationID: "res-empty-generation"}
			h := images.NewHandler(&mockAuthorizer{accountID: "acct-test", apiKeyID: "key-test"},
				&mockRouting{}, acc, mock.server.URL, "test-key", &mockStorage{}, "hive-images")

			req := httptest.NewRequest(http.MethodPost, "/v1/images/generations",
				strings.NewReader(`{"model":"hive-auto","prompt":"a single red circle","n":1}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-key")
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			assertImagelessRefusal(t, w, acc, "hive-auto")
		})
	}
}

func TestImageEditRefusesResponseWithNoImage(t *testing.T) {
	for _, tc := range imagelessUpstreamBodies {
		t.Run(tc.name, func(t *testing.T) {
			mock := newMockLiteLLM([]byte(tc.body), 200, "application/json")
			defer mock.Close()

			acc := &mockAccounting{reservationID: "res-empty-edit"}
			h := images.NewHandler(&mockAuthorizer{accountID: "acct-test", apiKeyID: "key-test"},
				&mockRouting{}, acc, mock.server.URL, "test-key", &mockStorage{}, "hive-images")

			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			_ = mw.WriteField("model", "hive-auto")
			_ = mw.WriteField("prompt", "make it blue")
			fw, _ := mw.CreateFormFile("image", "input.png")
			fw.Write([]byte("fake-image-bytes"))
			mw.Close()

			req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &buf)
			req.Header.Set("Content-Type", mw.FormDataContentType())
			req.Header.Set("Authorization", "Bearer test-key")
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			assertImagelessRefusal(t, w, acc, "hive-auto")
		})
	}
}
