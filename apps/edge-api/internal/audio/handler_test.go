package audio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/audio"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/stt"
)

// --- Test doubles ---

type mockLiteLLMAudio struct {
	server         *httptest.Server
	lastPath       string
	lastBody       []byte
	lastCT         string
	responseBody   []byte
	responseCode   int
	responseCtType string
}

func newMockLiteLLMAudio(body []byte, code int, ct string) *mockLiteLLMAudio {
	m := &mockLiteLLMAudio{
		responseBody:   body,
		responseCode:   code,
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

// mockAudioAuthorizer is a stub that always grants authorization.
type mockAudioAuthorizer struct {
	accountID string
	apiKeyID  string
}

func (a *mockAudioAuthorizer) AuthorizeRequest(_ *http.Request) (audio.AuthResult, error) {
	return audio.AuthResult{AccountID: a.accountID, APIKeyID: a.apiKeyID}, nil
}

// mockAudioRouting is a stub that echoes the alias as the LiteLLM model name.
type mockAudioRouting struct {
	litellmModel string
}

func (r *mockAudioRouting) SelectRoute(_ context.Context, input audio.RouteInput) (audio.RouteResult, error) {
	litellm := r.litellmModel
	if litellm == "" {
		litellm = input.AliasID
	}
	return audio.RouteResult{AliasID: input.AliasID, LiteLLMModelName: litellm}, nil
}

// mockAudioAccounting is a stub that tracks reservation calls.
type mockAudioAccounting struct {
	reservationID  string
	finalizeCalled bool
	releaseCalled  bool
}

func (a *mockAudioAccounting) CreateReservation(_ context.Context, _ audio.ReservationInput) (string, error) {
	return a.reservationID, nil
}

func (a *mockAudioAccounting) FinalizeReservation(_ context.Context, _ audio.FinalizeInput) error {
	a.finalizeCalled = true
	return nil
}

func (a *mockAudioAccounting) ReleaseReservation(_ context.Context, _, _, _ string) error {
	a.releaseCalled = true
	return nil
}

func buildAudioHandler(litellmBaseURL string) *audio.Handler {
	auth := &mockAudioAuthorizer{accountID: "acct-test", apiKeyID: "key-test"}
	routing := &mockAudioRouting{}
	accounting := &mockAudioAccounting{reservationID: "res-test"}
	return audio.NewHandler(auth, routing, accounting, litellmBaseURL, "test-key")
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
	// Transcription now routes to the STT backend directly (not LiteLLM).
	sttBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"text":"hello world","duration":5.2}`)) //nolint:errcheck
	}))
	defer sttBackend.Close()

	h := buildHandlerWithSTT(sttBackend.URL, sttBackend.URL)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "whisper-1")
	_ = mw.WriteField("language", "en")
	fw, _ := mw.CreateFormFile("file", "audio.mp3")
	fw.Write([]byte("fake-audio-bytes")) //nolint:errcheck
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
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
	// Transcription goes to STT backend directly; model field is forwarded as-is.
	var receivedBody []byte
	sttBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"text":"test transcription"}`)) //nolint:errcheck
	}))
	defer sttBackend.Close()

	h := buildHandlerWithSTT(sttBackend.URL, sttBackend.URL)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "whisper-1")
	fw, _ := mw.CreateFormFile("file", "audio.mp3")
	fw.Write([]byte("fake-audio")) //nolint:errcheck
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(string(receivedBody), "whisper-1") {
		t.Errorf("expected model field forwarded to STT backend, body: %s", receivedBody)
	}
}

func TestAudioNoStorageInTranscription(t *testing.T) {
	// Transcription must work without any storage field (Handler has none).
	sttBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"text":"no storage please"}`)) //nolint:errcheck
	}))
	defer sttBackend.Close()

	h := buildHandlerWithSTT(sttBackend.URL, sttBackend.URL)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "whisper-1")
	fw, _ := mw.CreateFormFile("file", "audio.mp3")
	fw.Write([]byte("audio-data")) //nolint:errcheck
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

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

func TestSpeechUpstreamFailureIsProviderBlind(t *testing.T) {
	raw := `{"error":{"message":"litellm.AuthenticationError: AuthenticationError: OpenrouterException: route-openrouter-default rejected openrouter/openrouter/free","type":"auth_error"}}`
	mock := newMockLiteLLMAudio([]byte(raw), http.StatusUnauthorized, "application/json")
	defer mock.Close()

	h := buildAudioHandler(mock.server.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"hive-fast","input":"Hello world","voice":"alloy"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}

	resp := decodeAudioError(t, w)
	assertAudioMessageProviderBlind(t, resp.Error.Message)
}

func TestTranscriptionUpstreamFailureIsProviderBlind(t *testing.T) {
	// STT backend returns an error; response must be provider-blind.
	sttBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"internal model error","type":"server_error"}}`)) //nolint:errcheck
	}))
	defer sttBackend.Close()

	h := buildHandlerWithSTT(sttBackend.URL, sttBackend.URL)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "hive-fast")
	fw, _ := mw.CreateFormFile("file", "audio.mp3")
	_, _ = fw.Write([]byte("fake-audio"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatal("expected non-200 on STT backend error")
	}
	resp := decodeAudioError(t, w)
	// Must not leak any backend identity.
	for _, forbidden := range []string{"parakeet", "faster", "whisper", "stt"} {
		if strings.Contains(strings.ToLower(resp.Error.Message), forbidden) {
			t.Errorf("provider-blind violation: found %q in message %q", forbidden, resp.Error.Message)
		}
	}
}

func decodeAudioError(t *testing.T, w *httptest.ResponseRecorder) apierrors.OpenAIError {
	t.Helper()

	var resp apierrors.OpenAIError
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	return resp
}

func assertAudioMessageProviderBlind(t *testing.T, message string) {
	t.Helper()

	lowerMessage := strings.ToLower(message)
	for _, forbidden := range []string{
		"openrouter",
		"groq",
		"litellm",
		"route-openrouter-default",
		"openrouter/auto",
		"openrouterexception",
		"authenticationerror",
	} {
		if strings.Contains(lowerMessage, forbidden) {
			t.Fatalf("expected provider-blind message, found %q in %q", forbidden, message)
		}
	}
}

// --- STT wiring tests ---

// buildHandlerWithSTT creates an audio.Handler wired to a real stt.TieredClient
// pointing at the given fake backend URL for English.
func buildHandlerWithSTT(parakeetURL, fasterWhisperURL string) *audio.Handler {
	auth := &mockAudioAuthorizer{accountID: "acct-test", apiKeyID: "key-test"}
	routing := &mockAudioRouting{}
	accounting := &mockAudioAccounting{reservationID: "res-test"}
	h := audio.NewHandler(auth, routing, accounting, "http://unused-litellm.example.com", "test-key")
	h.WithSTT(stt.NewTieredClient(stt.Config{
		ParakeetBaseURL:      parakeetURL,
		FasterWhisperBaseURL: fasterWhisperURL,
	}))
	return h
}

// TestTranscriptionRoutesToSTTNotLiteLLM proves that when WithSTT is set,
// POST /v1/audio/transcriptions hits the STT backend, not LiteLLM.
func TestTranscriptionRoutesToSTTNotLiteLLM(t *testing.T) {
	sttBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"text":"routed to stt"}`)) //nolint:errcheck
	}))
	defer sttBackend.Close()

	// LiteLLM must NOT be called; point it at a server that returns 500.
	liteLLMShouldNotBeCalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("LiteLLM was called at path %s — transcription must go to STT backend", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer liteLLMShouldNotBeCalled.Close()

	h := buildHandlerWithSTT(sttBackend.URL, sttBackend.URL)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "whisper-1")
	_ = mw.WriteField("language", "en")
	fw, _ := mw.CreateFormFile("file", "audio.wav")
	fw.Write([]byte("fake-audio")) //nolint:errcheck
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "routed to stt") {
		t.Errorf("expected STT response text, got: %s", w.Body.String())
	}
}

// TestTranscriptionForwardedBodyIsNonEmpty proves the audio bytes reach the
// backend (P2: drained-body fix).
func TestTranscriptionForwardedBodyIsNonEmpty(t *testing.T) {
	var receivedBody []byte
	sttBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"text":"ok"}`)) //nolint:errcheck
	}))
	defer sttBackend.Close()

	h := buildHandlerWithSTT(sttBackend.URL, sttBackend.URL)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "whisper-1")
	_ = mw.WriteField("language", "en")
	fw, _ := mw.CreateFormFile("file", "audio.wav")
	audioPayload := []byte("not-empty-audio-bytes-12345")
	fw.Write(audioPayload) //nolint:errcheck
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(receivedBody) == 0 {
		t.Error("backend received empty body: drained-body bug not fixed")
	}
	if !strings.Contains(string(receivedBody), "not-empty-audio-bytes-12345") {
		t.Errorf("audio bytes not present in forwarded body; got %d bytes", len(receivedBody))
	}
}

// TestTranscriptionWithNoSTTReturns503 proves that when WithSTT is not called,
// transcription returns 503 (no silent fallback to LiteLLM on sovereign box).
func TestTranscriptionWithNoSTTReturns503(t *testing.T) {
	// Handler without WithSTT — STT field is nil.
	h := buildAudioHandler("http://should-not-be-called.example.com")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "whisper-1")
	fw, _ := mw.CreateFormFile("file", "audio.wav")
	fw.Write([]byte("audio")) //nolint:errcheck
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when STT not configured, got %d: %s", w.Code, w.Body.String())
	}
}
