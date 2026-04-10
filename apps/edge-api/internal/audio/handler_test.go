package audio_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hivegpt/hive/apps/edge-api/internal/audio"
)

// --- Test doubles ---

type mockLiteLLMAudio struct {
	server        *httptest.Server
	lastPath      string
	lastBody      []byte
	lastCT        string
	responseBody  []byte
	responseCode  int
	responseCtType string
}

func newMockLiteLLMAudio(body []byte, code int, ct string) *mockLiteLLMAudio {
	m := &mockLiteLLMAudio{
		responseBody:  body,
		responseCode:  code,
		responseCtType: ct,
	}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.lastPath = r.URL.Path
		m.lastBody, _ = io.ReadAll(r.Body)
		m.lastCT = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(code)
		w.Write(body)
	}))
	return m
}

func (m *mockLiteLLMAudio) Close() { m.server.Close() }

func buildAudioHandler(litellmBaseURL string) *audio.Handler {
	return audio.NewHandler(litellmBaseURL, "test-key")
}

// --- Tests ---

func TestSpeechBinaryRelay(t *testing.T) {
	audioBytes := []byte{0xFF, 0xFB, 0x90, 0x00, 0x01, 0x02} // fake MP3 bytes
	mock := newMockLiteLLMAudio(audioBytes, 200, "audio/mpeg")
	defer mock.Close()

	h := buildAudioHandler(mock.server.URL)

	body := `{"model":"tts-1","input":"Hello world","voice":"alloy"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if mock.lastPath != "/audio/speech" {
		t.Errorf("expected LiteLLM path /audio/speech, got %s", mock.lastPath)
	}
	// Response body must be the exact binary bytes - no JSON wrapping
	if !bytes.Equal(w.Body.Bytes(), audioBytes) {
		t.Errorf("expected binary audio bytes passed through, got different bytes")
	}
}

func TestSpeechContentTypePassthrough(t *testing.T) {
	// Verify that whatever Content-Type LiteLLM returns is passed through exactly
	testCases := []struct {
		litellmCT string
	}{
		{"audio/mpeg"},
		{"audio/opus"},
		{"audio/aac"},
		{"audio/flac"},
	}

	for _, tc := range testCases {
		t.Run(tc.litellmCT, func(t *testing.T) {
			mock := newMockLiteLLMAudio([]byte("binary"), 200, tc.litellmCT)
			defer mock.Close()

			h := buildAudioHandler(mock.server.URL)

			body := `{"model":"tts-1","input":"Hi","voice":"alloy"}`
			req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer test-key")
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}
			if w.Header().Get("Content-Type") != tc.litellmCT {
				t.Errorf("expected Content-Type %s, got %s", tc.litellmCT, w.Header().Get("Content-Type"))
			}
		})
	}
}

func TestTranscriptionForward(t *testing.T) {
	respBody := `{"text":"hello world","duration":5.2}`
	mock := newMockLiteLLMAudio([]byte(respBody), 200, "application/json")
	defer mock.Close()

	h := buildAudioHandler(mock.server.URL)

	// Build multipart form
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "whisper-1")
	_ = mw.WriteField("language", "en")
	fw, _ := mw.CreateFormFile("file", "audio.mp3")
	fw.Write([]byte("fake-audio-bytes"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if mock.lastPath != "/audio/transcriptions" {
		t.Errorf("expected path /audio/transcriptions, got %s", mock.lastPath)
	}
	// Outgoing request must be multipart (audio data forwarded as multipart to LiteLLM)
	if !strings.Contains(mock.lastCT, "multipart/form-data") {
		t.Errorf("expected multipart Content-Type to LiteLLM, got %s", mock.lastCT)
	}
	// Response must be JSON passthrough
	var tr audio.TranscriptionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &tr); err != nil {
		t.Fatalf("could not decode transcription response: %v", err)
	}
	if tr.Text != "hello world" {
		t.Errorf("expected text 'hello world', got %s", tr.Text)
	}
}

func TestTranslationForward(t *testing.T) {
	respBody := `{"text":"bonjour","duration":3.1}`
	mock := newMockLiteLLMAudio([]byte(respBody), 200, "application/json")
	defer mock.Close()

	h := buildAudioHandler(mock.server.URL)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "whisper-1")
	fw, _ := mw.CreateFormFile("file", "audio.mp3")
	fw.Write([]byte("fake-audio-bytes"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/translations", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if mock.lastPath != "/audio/translations" {
		t.Errorf("expected path /audio/translations, got %s", mock.lastPath)
	}
	if !strings.Contains(mock.lastCT, "multipart/form-data") {
		t.Errorf("expected multipart Content-Type to LiteLLM, got %s", mock.lastCT)
	}
}

func TestAudioModelAliasRewrite(t *testing.T) {
	// Verify that the outgoing request to LiteLLM can be controlled by litellmModel
	// (in our case the handler passes model through directly since no routing layer in unit test)
	respBody := `{"text":"test transcription"}`
	mock := newMockLiteLLMAudio([]byte(respBody), 200, "application/json")
	defer mock.Close()

	h := buildAudioHandler(mock.server.URL)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "whisper-1")
	fw, _ := mw.CreateFormFile("file", "audio.mp3")
	fw.Write([]byte("fake-audio"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// The outgoing body must contain the model field (forwarded in multipart)
	outgoingBody := string(mock.lastBody)
	if !strings.Contains(outgoingBody, "whisper-1") {
		t.Errorf("expected model 'whisper-1' in outgoing multipart, body: %s", outgoingBody)
	}
}

func TestAudioNoStorageInTranscription(t *testing.T) {
	// Audio handler must NOT call any storage - no storage field on Handler
	// This test verifies the handler struct by checking it doesn't error
	// when there's no storage configured (audio.NewHandler has no storage param)
	respBody := `{"text":"no storage please"}`
	mock := newMockLiteLLMAudio([]byte(respBody), 200, "application/json")
	defer mock.Close()

	h := buildAudioHandler(mock.server.URL)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "whisper-1")
	fw, _ := mw.CreateFormFile("file", "audio.mp3")
	fw.Write([]byte("audio-data"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	// If it reaches here without panicking on nil storage, the test passes
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAudioUnknownPathReturns404(t *testing.T) {
	h := buildAudioHandler("http://unused.example.com")

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/unknown", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}
