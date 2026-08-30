package audio_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestAudioTranslationRetriesUpstream429ThenSucceeds is the audio half of
// issue #1564's fix. handleMultipartAudio (apps/edge-api/internal/audio/handler.go)
// made a single bare h.httpClient.Do call to LiteLLM with no retry at all,
// the same defect the JWT chat surface had. This exercises the
// /v1/audio/translations path, which always forwards through LiteLLM (unlike
// transcription, which prefers a configured STT sidecar), and proves the
// handler now retries a 429 and succeeds on a later attempt instead of
// failing the request outright.
func TestAudioTranslationRetriesUpstream429ThenSucceeds(t *testing.T) {
	var attempts int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited: no fallback model group found"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"text":"bonjour","duration":3.1}`))
	}))
	defer upstream.Close()

	h := buildAudioHandler(upstream.URL)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "whisper-1")
	fw, _ := mw.CreateFormFile("file", "audio.mp3")
	_, _ = fw.Write([]byte("fake-audio-bytes"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/translations", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected the first 429 to be retried and the later 200 to be returned, got %d: %s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&attempts); got < 2 {
		t.Fatalf("expected the handler to retry the upstream after the first 429, got %d attempt(s)", got)
	}
}
