package audio_test

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// buildTestWAV constructs a minimal canonical 44-byte-header PCM WAV file
// wrapping the given sample bytes, matching the shape Groq's Orpheus TTS
// backend returns for response_format=wav.
func buildTestWAV(samples []byte) []byte {
	dataLen := len(samples)
	buf := make([]byte, 44+dataLen)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataLen)) //nolint:gosec
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)     // PCM
	binary.LittleEndian.PutUint16(buf[22:24], 1)     // mono
	binary.LittleEndian.PutUint32(buf[24:28], 24000) // sample rate
	binary.LittleEndian.PutUint32(buf[28:32], 48000) // byte rate
	binary.LittleEndian.PutUint16(buf[32:34], 2)     // block align
	binary.LittleEndian.PutUint16(buf[34:36], 16)    //nolint:gosec // bits per sample
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataLen)) //nolint:gosec
	copy(buf[44:], samples)
	return buf
}

func silentTestWAV(n int) []byte {
	return buildTestWAV(make([]byte, n))
}

func speechTestWAV(n int) []byte {
	samples := make([]byte, n)
	for i := range samples {
		samples[i] = byte(i%200 + 1) // any non-zero pattern
	}
	return buildTestWAV(samples)
}

// sequencedSpeechServer serves one response body per call in order, from
// responses, repeating the last entry once exhausted. It counts calls.
type sequencedSpeechServer struct {
	server    *httptest.Server
	responses [][]byte
	calls     atomic.Int32
}

func newSequencedSpeechServer(responses [][]byte) *sequencedSpeechServer {
	s := &sequencedSpeechServer{responses: responses}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(s.calls.Add(1)) - 1
		body := s.responses[len(s.responses)-1]
		if n < len(s.responses) {
			body = s.responses[n]
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		w.Write(body) //nolint:errcheck
	}))
	return s
}

func (s *sequencedSpeechServer) Close() { s.server.Close() }

func TestSpeechRetriesOnSilentWAVThenSucceeds(t *testing.T) {
	good := speechTestWAV(2000)
	mock := newSequencedSpeechServer([][]byte{silentTestWAV(2000), good})
	defer mock.Close()

	h := buildAudioHandler(mock.server.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"hive-tts","input":"hello","voice":"alloy","response_format":"wav"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != len(good) {
		t.Fatalf("expected the good (second attempt) WAV of length %d, got %d bytes", len(good), w.Body.Len())
	}
	if got := mock.calls.Load(); got != 2 {
		t.Errorf("expected exactly 2 upstream calls (silent then good), got %d", got)
	}
}

func TestSpeechFailsAfterAllAttemptsSilent(t *testing.T) {
	silent := silentTestWAV(2000)
	mock := newSequencedSpeechServer([][]byte{silent, silent, silent})
	defer mock.Close()

	h := buildAudioHandler(mock.server.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"hive-tts","input":"hello","voice":"alloy","response_format":"wav"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 once every retry attempt comes back silent, got %d: %s", w.Code, w.Body.String())
	}
	if got := mock.calls.Load(); got != 3 {
		t.Errorf("expected exactly 3 upstream attempts (maxSpeechAttempts), got %d", got)
	}
}

func TestSpeechNonWAVResponseIsNeverRetried(t *testing.T) {
	// A non-WAV RESPONSE can't be validated for silence by this package, so it
	// must be relayed on the first attempt regardless of content, even if the
	// bytes happen to be all zero.
	//
	// The request used to ask for response_format mp3 to arrange that. It no
	// longer can: mp3 is refused at the request boundary now, because the route
	// cannot produce it (#1381). Nothing about what this test covers changes,
	// since the shape being exercised is the upstream's response body and the
	// sequenced mock decides that on its own, whatever the request asked for.
	allZeroMP3 := make([]byte, 500)
	mock := newSequencedSpeechServer([][]byte{allZeroMP3, speechTestWAV(2000)})
	defer mock.Close()

	h := buildAudioHandler(mock.server.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"hive-tts","input":"hello","voice":"alloy","response_format":"wav"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != len(allZeroMP3) {
		t.Fatalf("expected the first (only) attempt's body relayed unchanged, got %d bytes", w.Body.Len())
	}
	if got := mock.calls.Load(); got != 1 {
		t.Errorf("expected exactly 1 upstream call (non-WAV responses are never retried), got %d", got)
	}
}
