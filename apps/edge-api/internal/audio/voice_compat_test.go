package audio_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/audio"
)

// Issue #1318: POST /v1/audio/speech forwarded the caller voice name verbatim
// to a provider whose roster is entirely different, so every OpenAI SDK
// example was refused upstream and the refusal reached the caller as a 500.
//
// Both halves are asserted on the SERIALIZED wire: the outbound body the
// upstream actually receives, and the status plus JSON body the caller
// actually receives.

// outboundVoice reads the voice field out of the body the handler dispatched.
func outboundVoice(t *testing.T, raw []byte) string {
	t.Helper()
	var sent map[string]any
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatalf("dispatched body is not valid JSON: %v (%s)", err, string(raw))
	}
	voice, _ := sent["voice"].(string)
	return voice
}

// advertisedVoiceIDs is what GET /v1/audio/voices publishes, read through the
// handler rather than the package variable so the assertion covers the same
// bytes a client dropdown reads.
func advertisedVoiceIDs(t *testing.T) []string {
	t.Helper()
	rec := httptest.NewRecorder()
	audio.VoicesHandler()(rec, httptest.NewRequest(http.MethodGet, "/v1/audio/voices", nil))
	var body struct {
		Voices []struct {
			ID string `json:"id"`
		} `json:"voices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("voices response is not valid JSON: %v (%s)", err, rec.Body.String())
	}
	ids := make([]string, 0, len(body.Voices))
	for _, v := range body.Voices {
		ids = append(ids, v.ID)
	}
	if len(ids) == 0 {
		t.Fatal("GET /v1/audio/voices advertised nothing")
	}
	return ids
}

// TestSpeechTranslatesOpenAIStockVoices pins the compatibility promise: an
// unmodified OpenAI SDK call reaches the upstream with a voice the upstream
// accepts. Asserted on the dispatched bytes, because a handler that validated
// the name and then forwarded the original would pass a status-only check.
func TestSpeechTranslatesOpenAIStockVoices(t *testing.T) {
	supported := map[string]bool{}
	for _, id := range advertisedVoiceIDs(t) {
		supported[id] = true
	}

	for _, requested := range []string{"alloy", "ash", "ballad", "coral", "echo", "fable", "nova", "onyx", "sage", "shimmer", "verse", "ALLOY", " alloy "} {
		t.Run(requested, func(t *testing.T) {
			mock := newMockLiteLLMAudio([]byte("binary-audio"), 200, "audio/mpeg")
			defer mock.Close()
			h := buildAudioHandler(mock.server.URL)

			body, err := json.Marshal(map[string]any{"model": "hive-tts", "input": "hello", "voice": requested})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-key")
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
			}
			sentVoice := outboundVoice(t, mock.lastBody)
			if !supported[sentVoice] {
				t.Fatalf("dispatched voice %q is not one the upstream accepts (sent %q)", sentVoice, requested)
			}
		})
	}
}

// TestSpeechAcceptsEveryAdvertisedVoice is the agreement check between
// GET /v1/audio/voices and the speech validator: a client that reads the
// roster and sends one back must never be refused, and the name must reach
// the upstream unchanged.
//
// It sends each id UPPERCASED, which is what keeps it able to go red
// (Antigravity review finding on this pull request). Sent verbatim, an
// advertised name would arrive at the mock unchanged with the guard deleted
// entirely, so the test would pass over a removed guard. Only a name the
// handler had to normalize proves the resolution step ran at all.
func TestSpeechAcceptsEveryAdvertisedVoice(t *testing.T) {
	for _, id := range advertisedVoiceIDs(t) {
		t.Run(id, func(t *testing.T) {
			mock := newMockLiteLLMAudio([]byte("binary-audio"), 200, "audio/mpeg")
			defer mock.Close()
			h := buildAudioHandler(mock.server.URL)

			body, err := json.Marshal(map[string]any{"model": "hive-tts", "input": "hello", "voice": strings.ToUpper(id)})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(string(body)))
			req.Header.Set("Authorization", "Bearer test-key")
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("advertised voice %q was refused: %d (body: %s)", id, w.Code, w.Body.String())
			}
			if sent := outboundVoice(t, mock.lastBody); sent != id {
				t.Fatalf("dispatched voice = %q, want the advertised %q", sent, id)
			}
		})
	}
}

// TestSpeechRefusesUnknownVoiceWithA4xx is the status half of #1318: an
// unsupported voice is the caller mistake it looks like. A 5xx here told the
// caller the gateway was broken and invited a retry that can never succeed.
func TestSpeechRefusesUnknownVoiceWithA4xx(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"unknown name", `{"model":"hive-tts","input":"hello","voice":"not-a-real-voice"}`},
		{"voice omitted", `{"model":"hive-tts","input":"hello"}`},
		{"voice empty", `{"model":"hive-tts","input":"hello","voice":"   "}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newMockLiteLLMAudio([]byte("binary-audio"), 200, "audio/mpeg")
			defer mock.Close()

			auth := &mockAudioAuthorizer{accountID: "acct-test", apiKeyID: "key-test"}
			acc := &mockAudioAccounting{reservationID: "res-voice"}
			h := audio.NewHandler(auth, &mockAudioRouting{}, acc, mock.server.URL, "test-key")

			req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-key")
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
			}
			var envelope struct {
				Error struct {
					Message string  `json:"message"`
					Type    string  `json:"type"`
					Param   *string `json:"param"`
					Code    *string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("refusal is not valid JSON: %v (%s)", err, w.Body.String())
			}
			if envelope.Error.Type != "invalid_request_error" {
				t.Errorf("type = %q, want invalid_request_error", envelope.Error.Type)
			}
			if envelope.Error.Param == nil || *envelope.Error.Param != "voice" {
				t.Errorf("param = %v, want voice: an SDK caller needs to know which field to fix", envelope.Error.Param)
			}
			if envelope.Error.Code == nil || *envelope.Error.Code != "invalid_value" {
				t.Errorf("code = %v, want invalid_value", envelope.Error.Code)
			}
			for _, id := range advertisedVoiceIDs(t) {
				if !strings.Contains(envelope.Error.Message, id) {
					t.Errorf("message does not name the supported voice %q: %s", id, envelope.Error.Message)
				}
			}
			if mock.lastBody != nil {
				t.Errorf("an unsupported voice was still dispatched upstream: %s", string(mock.lastBody))
			}
			if acc.createCalled {
				t.Error("a reservation was taken for a request that cannot succeed")
			}
		})
	}
}
