package audio_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Issue #1381: POST /v1/audio/speech forwarded the caller's response_format
// verbatim, and forwarded nothing at all when the caller omitted it. The
// OpenAI SDK omits it, the OpenAI default is mp3, and the Groq Orpheus route
// behind hive-tts produces wav only, so every stock SDK call and every Open
// WebUI read-aloud reached the provider asking for a format it refuses.
// LiteLLM classified the refusal as a BadRequestError and answered 500 anyway,
// so the caller saw "hive-tts request failed." and the real sentence,
// "response_format must be one of [wav]", stayed in the LiteLLM log.
//
// Same shape as the voice defect (#1318) and the same remedy: resolve the
// parameter against what the route actually accepts before spending anything,
// coerce the absent case, and refuse an explicitly unsupported one with a 400
// that names the parameter.
//
// Everything below asserts on the SERIALIZED wire, the body the upstream
// receives and the body the caller receives, because a handler that validated
// the value and then dispatched something else would pass a status-only check.

// outboundResponseFormat reads response_format out of the dispatched body.
// present=false distinguishes "the field was omitted" from "the field was sent
// empty", which is the exact distinction this defect turned on.
func outboundResponseFormat(t *testing.T, raw []byte) (value string, present bool) {
	t.Helper()
	var sent map[string]any
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatalf("dispatched body is not valid JSON: %v (%s)", err, string(raw))
	}
	v, ok := sent["response_format"]
	if !ok {
		return "", false
	}
	s, isString := v.(string)
	if !isString {
		t.Fatalf("dispatched response_format is %T, want string (%s)", v, string(raw))
	}
	return s, true
}

func postSpeechBody(t *testing.T, mockURL string, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	buildAudioHandler(mockURL).ServeHTTP(w, req)
	return w
}

// TestSpeechDefaultsResponseFormatToOneTheRouteAccepts is the stock-SDK case,
// and the one that was failing in production. The caller says nothing about
// the format, so the handler must say something rather than let the upstream
// fall back to its own default.
func TestSpeechDefaultsResponseFormatToOneTheRouteAccepts(t *testing.T) {
	supported := map[string]bool{}
	for _, f := range supportedSpeechFormatsForTest(t) {
		supported[f] = true
	}

	mock := newMockLiteLLMAudio([]byte("RIFFxxxxWAVE"), 200, "audio/wav")
	defer mock.Close()

	w := postSpeechBody(t, mock.server.URL, map[string]any{
		"model": "hive-tts",
		"input": "hello",
		"voice": "autumn",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	got, present := outboundResponseFormat(t, mock.lastBody)
	if !present {
		t.Fatalf("handler dispatched no response_format, so the upstream falls back to its own default and refuses the request (body: %s)", string(mock.lastBody))
	}
	if !supported[got] {
		t.Fatalf("dispatched response_format = %q, which the route does not accept; want one of %v", got, supportedSpeechFormatsForTest(t))
	}
}

// TestSpeechAcceptsSupportedFormatNormalized proves the resolution step runs
// rather than the value merely surviving. Each case is sent in a spelling the
// handler has to normalize, so deleting the resolver entirely turns this red
// instead of leaving it accidentally green.
func TestSpeechAcceptsSupportedFormatNormalized(t *testing.T) {
	for _, canonical := range supportedSpeechFormatsForTest(t) {
		for _, spelling := range []string{strings.ToUpper(canonical), " " + canonical + " "} {
			t.Run(spelling, func(t *testing.T) {
				mock := newMockLiteLLMAudio([]byte("RIFFxxxxWAVE"), 200, "audio/wav")
				defer mock.Close()

				w := postSpeechBody(t, mock.server.URL, map[string]any{
					"model":           "hive-tts",
					"input":           "hello",
					"voice":           "autumn",
					"response_format": spelling,
				})

				if w.Code != http.StatusOK {
					t.Fatalf("supported format %q was refused: %d (body: %s)", spelling, w.Code, w.Body.String())
				}
				got, present := outboundResponseFormat(t, mock.lastBody)
				if !present || got != canonical {
					t.Fatalf("dispatched response_format = %q (present=%v), want the normalized %q", got, present, canonical)
				}
			})
		}
	}
}

// TestSpeechRefusesUnsupportedFormatWithA4xx is the status half. An
// unsupported format is a caller mistake, so it is answered as one: a 400 that
// names the parameter and the supported set, never a 5xx, and never after a
// reservation has been taken for a request that cannot succeed.
func TestSpeechRefusesUnsupportedFormatWithA4xx(t *testing.T) {
	for _, requested := range []string{"mp3", "opus", "aac", "flac", "pcm", "not-a-format"} {
		t.Run(requested, func(t *testing.T) {
			mock := newMockLiteLLMAudio([]byte("binary-audio"), 200, "audio/mpeg")
			defer mock.Close()
			acc := &mockAudioAccounting{reservationID: "res-test"}
			body, err := json.Marshal(map[string]any{
				"model":           "hive-tts",
				"input":           "hello",
				"voice":           "autumn",
				"response_format": requested,
			})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-key")
			w := httptest.NewRecorder()
			buildAudioHandlerWithAccounting(mock.server.URL, acc).ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
			}
			var errBody struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
					Param   string `json:"param"`
					Code    string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &errBody); err != nil {
				t.Fatalf("refusal is not valid JSON: %v (%s)", err, w.Body.String())
			}
			if errBody.Error.Type != "invalid_request_error" {
				t.Fatalf("error.type = %q, want invalid_request_error", errBody.Error.Type)
			}
			if errBody.Error.Param != "response_format" {
				t.Fatalf("error.param = %q, want response_format", errBody.Error.Param)
			}
			for _, f := range supportedSpeechFormatsForTest(t) {
				if !strings.Contains(errBody.Error.Message, f) {
					t.Fatalf("refusal message %q does not name the supported format %q", errBody.Error.Message, f)
				}
			}
			if acc.createCalled {
				t.Fatal("a reservation was taken for a request that cannot succeed")
			}
			if mock.lastBody != nil {
				t.Fatalf("the refused request was still dispatched upstream (%s)", string(mock.lastBody))
			}
		})
	}
}

// supportedSpeechFormatsForTest reads the roster the refusal message
// advertises, so the assertions above cannot drift from what the handler
// actually accepts. It is derived from a refusal rather than from the package
// variable for the same reason advertisedVoiceIDs reads the voices endpoint:
// the test sees what a client sees.
func supportedSpeechFormatsForTest(t *testing.T) []string {
	t.Helper()
	mock := newMockLiteLLMAudio([]byte("binary-audio"), 200, "audio/mpeg")
	defer mock.Close()

	w := postSpeechBody(t, mock.server.URL, map[string]any{
		"model":           "hive-tts",
		"input":           "hello",
		"voice":           "autumn",
		"response_format": "definitely-not-a-format",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("an unsupported response_format was not refused with a 400: %d (body: %s)", w.Code, w.Body.String())
	}
	var errBody struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("refusal is not valid JSON: %v (%s)", err, w.Body.String())
	}
	_, list, found := strings.Cut(errBody.Error.Message, "Supported formats are: ")
	if !found {
		t.Fatalf("refusal message %q does not advertise the supported formats", errBody.Error.Message)
	}
	formats := strings.Split(strings.TrimSuffix(strings.TrimSpace(list), "."), ", ")
	if len(formats) == 0 || formats[0] == "" {
		t.Fatalf("refusal message %q advertised an empty format roster", errBody.Error.Message)
	}
	return formats
}
