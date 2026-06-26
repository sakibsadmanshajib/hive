package stt_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/stt"
)

// fakeSTTBackend records the last request it received and returns a fixed response.
type fakeSTTBackend struct {
	server   *httptest.Server
	lastPath string
	lastCT   string
	lastBody []byte
	respBody string
	respCode int
}

func newFakeSTTBackend(respBody string, code int) *fakeSTTBackend {
	f := &fakeSTTBackend{respBody: respBody, respCode: code}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.lastPath = r.URL.Path
		f.lastCT = r.Header.Get("Content-Type")
		f.lastBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.respCode)
		w.Write([]byte(f.respBody)) //nolint:errcheck
	}))
	return f
}

func (f *fakeSTTBackend) Close() { f.server.Close() }

// buildMultipart builds a minimal multipart transcription request body.
func buildMultipart(t *testing.T, language string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "whisper-1")
	if language != "" {
		_ = mw.WriteField("language", language)
	}
	fw, _ := mw.CreateFormFile("file", "audio.wav")
	_, _ = fw.Write([]byte("fake-audio-bytes"))
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

// --- Language routing ---

func TestEnglishRoutesToParakeet(t *testing.T) {
	parakeet := newFakeSTTBackend(`{"text":"hello"}`, http.StatusOK)
	defer parakeet.Close()
	faster := newFakeSTTBackend(`{"text":"should not be called"}`, http.StatusOK)
	defer faster.Close()

	c := stt.NewTieredClient(stt.Config{
		ParakeetBaseURL:      parakeet.server.URL,
		FasterWhisperBaseURL: faster.server.URL,
	})

	body, ct := buildMultipart(t, "en")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()

	c.Transcribe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if parakeet.lastPath == "" {
		t.Error("expected parakeet backend to be called for English")
	}
	if faster.lastPath != "" {
		t.Error("expected faster-whisper backend NOT to be called for English")
	}
	if !strings.Contains(w.Body.String(), "hello") {
		t.Errorf("expected transcription text in response, got: %s", w.Body.String())
	}
}

func TestBanglaRoutesToFasterWhisper(t *testing.T) {
	parakeet := newFakeSTTBackend(`{"text":"should not be called"}`, http.StatusOK)
	defer parakeet.Close()
	faster := newFakeSTTBackend(`{"text":"আমি ভালো আছি"}`, http.StatusOK)
	defer faster.Close()

	c := stt.NewTieredClient(stt.Config{
		ParakeetBaseURL:      parakeet.server.URL,
		FasterWhisperBaseURL: faster.server.URL,
	})

	body, ct := buildMultipart(t, "bn")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()

	c.Transcribe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if faster.lastPath == "" {
		t.Error("expected faster-whisper backend to be called for Bangla")
	}
	if parakeet.lastPath != "" {
		t.Error("expected parakeet backend NOT to be called for Bangla")
	}
}

func TestUnknownLanguageRoutesToFasterWhisper(t *testing.T) {
	parakeet := newFakeSTTBackend(`{"text":"should not be called"}`, http.StatusOK)
	defer parakeet.Close()
	faster := newFakeSTTBackend(`{"text":"bonjour"}`, http.StatusOK)
	defer faster.Close()

	c := stt.NewTieredClient(stt.Config{
		ParakeetBaseURL:      parakeet.server.URL,
		FasterWhisperBaseURL: faster.server.URL,
	})

	body, ct := buildMultipart(t, "fr")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()

	c.Transcribe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if faster.lastPath == "" {
		t.Error("expected faster-whisper for non-English language")
	}
	if parakeet.lastPath != "" {
		t.Error("expected parakeet NOT called for French")
	}
}

func TestEmptyLanguageRoutesToFasterWhisper(t *testing.T) {
	parakeet := newFakeSTTBackend(`{"text":"should not be called"}`, http.StatusOK)
	defer parakeet.Close()
	faster := newFakeSTTBackend(`{"text":"auto detect"}`, http.StatusOK)
	defer faster.Close()

	c := stt.NewTieredClient(stt.Config{
		ParakeetBaseURL:      parakeet.server.URL,
		FasterWhisperBaseURL: faster.server.URL,
	})

	// No language field: auto-detect path goes to faster-whisper (multilingual).
	body, ct := buildMultipart(t, "")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()

	c.Transcribe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if faster.lastPath == "" {
		t.Error("expected faster-whisper for auto-detect (empty language)")
	}
}

// --- Missing backend config ---

func TestMissingParakeetURLReturns503ForEnglish(t *testing.T) {
	faster := newFakeSTTBackend(`{"text":"ok"}`, http.StatusOK)
	defer faster.Close()

	// Parakeet URL intentionally unset.
	c := stt.NewTieredClient(stt.Config{
		ParakeetBaseURL:      "",
		FasterWhisperBaseURL: faster.server.URL,
	})

	body, ct := buildMultipart(t, "en")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()

	c.Transcribe(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when parakeet URL unset, got %d: %s", w.Code, w.Body.String())
	}
	// Response must be provider-blind (no internal service names).
	resp := w.Body.String()
	for _, forbidden := range []string{"parakeet", "faster", "whisper", "stt"} {
		if strings.Contains(strings.ToLower(resp), forbidden) {
			t.Errorf("provider-blind violation: found %q in %q", forbidden, resp)
		}
	}
}

func TestMissingFasterWhisperURLReturns503ForBangla(t *testing.T) {
	parakeet := newFakeSTTBackend(`{"text":"ok"}`, http.StatusOK)
	defer parakeet.Close()

	// FasterWhisper URL intentionally unset.
	c := stt.NewTieredClient(stt.Config{
		ParakeetBaseURL:      parakeet.server.URL,
		FasterWhisperBaseURL: "",
	})

	body, ct := buildMultipart(t, "bn")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()

	c.Transcribe(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when faster-whisper URL unset, got %d: %s", w.Code, w.Body.String())
	}
	resp := w.Body.String()
	for _, forbidden := range []string{"parakeet", "faster", "whisper", "stt"} {
		if strings.Contains(strings.ToLower(resp), forbidden) {
			t.Errorf("provider-blind violation: found %q in %q", forbidden, resp)
		}
	}
}

func TestBothURLsMissingReturns503(t *testing.T) {
	c := stt.NewTieredClient(stt.Config{})

	body, ct := buildMultipart(t, "en")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()

	c.Transcribe(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when all URLs unset, got %d", w.Code)
	}
}

// --- Backend error mapping ---

func TestBackendErrorIsProviderBlind(t *testing.T) {
	// Backend returns a provider-leaking internal error message.
	rawErr := `{"error":{"message":"model file not found at /models/internal.onnx","type":"server_error"}}`
	parakeet := newFakeSTTBackend(rawErr, http.StatusInternalServerError)
	defer parakeet.Close()
	faster := newFakeSTTBackend(`{}`, http.StatusOK)
	defer faster.Close()

	c := stt.NewTieredClient(stt.Config{
		ParakeetBaseURL:      parakeet.server.URL,
		FasterWhisperBaseURL: faster.server.URL,
	})

	body, ct := buildMultipart(t, "en")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()

	c.Transcribe(w, req)

	if w.Code == http.StatusOK {
		t.Fatal("expected non-200 on backend 500 error")
	}
	respBody := w.Body.String()
	for _, forbidden := range []string{"parakeet", "faster", "whisper", ".onnx", "/models/"} {
		if strings.Contains(strings.ToLower(respBody), strings.ToLower(forbidden)) {
			t.Errorf("provider-blind violation: found %q in response %q", forbidden, respBody)
		}
	}
}

// --- Multipart forwarding ---

func TestMultipartForwardedToBackend(t *testing.T) {
	parakeet := newFakeSTTBackend(`{"text":"forwarded ok"}`, http.StatusOK)
	defer parakeet.Close()
	faster := newFakeSTTBackend(`{}`, http.StatusOK)
	defer faster.Close()

	c := stt.NewTieredClient(stt.Config{
		ParakeetBaseURL:      parakeet.server.URL,
		FasterWhisperBaseURL: faster.server.URL,
	})

	body, ct := buildMultipart(t, "en")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()

	c.Transcribe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(parakeet.lastCT, "multipart/form-data") {
		t.Errorf("expected multipart forwarded to backend, got Content-Type: %s", parakeet.lastCT)
	}
}

// --- Response passthrough ---

func TestResponseJSONPassthrough(t *testing.T) {
	respJSON := `{"text":"passthrough check","duration":3.14}`
	parakeet := newFakeSTTBackend(respJSON, http.StatusOK)
	defer parakeet.Close()
	faster := newFakeSTTBackend(`{}`, http.StatusOK)
	defer faster.Close()

	c := stt.NewTieredClient(stt.Config{
		ParakeetBaseURL:      parakeet.server.URL,
		FasterWhisperBaseURL: faster.server.URL,
	})

	body, ct := buildMultipart(t, "en")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()

	c.Transcribe(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected application/json, got %s", w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "passthrough check") {
		t.Errorf("expected response text in body, got: %s", w.Body.String())
	}
}
