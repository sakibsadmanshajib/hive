//go:build integration

package audio_test

// Live voice round trip — the coverage gap this file closes: handler_test.go
// exercises the audio Handler's request/response plumbing exhaustively, but
// always against a fake upstream (httptest.Server returning a canned body).
// Nothing anywhere called a real STT/TTS provider and checked the words that
// came back. This test speaks a known sentence through a real TTS provider,
// feeds the resulting audio into a real Whisper-compatible STT provider, and
// asserts the recovered text actually matches. Both legs run the production
// audio.Handler unmodified; only auth/routing/accounting are stubbed (those
// three are already covered exhaustively in handler_test.go against a fake
// upstream, so restubbing them here would add nothing).
//
// IMPORTANT — why this test points at Groq directly instead of through
// LiteLLM, unlike Hive's real deployment: while writing this test, live
// verification found that LiteLLM v1.77.7-stable and main-stable both proxy
// /audio/speech and /audio/transcriptions to Groq without erroring, but
// silently corrupt the binary audio payload in both directions (200 OK,
// correct byte length, but PCM samples come back all-zero on the way out and
// Whisper transcribes garbage — " you" — on the way in), while an identical
// request straight to Groq's own API works perfectly. That is a real,
// separate infrastructure defect (outside Hive's own Go code, in the pinned
// LiteLLM proxy layer) and is reported alongside this change rather than
// fixed here — fixing it is out of scope for a test-coverage pass. Pointing
// this test at LiteLLM would make it fail for that reason instead of
// verifying what it is meant to verify: that audio.Handler's own dispatch,
// multipart handling, and response passthrough are correct end to end
// against a real provider. Hive's provider-agnostic design means the handler
// code under test here is identical either way; only the base URL changes.
//
// Prerequisites:
//   - A real GROQ_API_KEY (voice-groq-stt-tts routes: deploy/litellm/config.yaml).
//   - HIVE_AUDIO_LIVE_TEST=1 to opt in. This calls a real provider and costs
//     real (free-tier) API usage, so it never runs as part of the normal
//     `go test ./...` suite or CI's go-tests job.
//
// Run (from deploy/docker toolchain container, with GROQ_API_KEY exported):
//
//	docker compose run toolchain "cd /workspace && \
//	  HIVE_AUDIO_LIVE_TEST=1 GROQ_API_KEY=<real key> \
//	  go test -tags integration ./apps/edge-api/internal/audio/... -run TestLiveVoiceRoundTrip -v"
import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/audio"
)

// liveRoutingStub pins every SelectRoute call to one real provider model
// name, standing in for the alias -> route mapping that migration
// 20260717_02 seeds into provider_routes for the normal (LiteLLM) path.
type liveRoutingStub struct{ providerModel string }

func (r liveRoutingStub) SelectRoute(_ context.Context, input audio.RouteInput) (audio.RouteResult, error) {
	return audio.RouteResult{AliasID: input.AliasID, LiteLLMModelName: r.providerModel}, nil
}

type liveAuthStub struct{}

func (liveAuthStub) AuthorizeRequest(_ *http.Request) (audio.AuthResult, error) {
	return audio.AuthResult{AccountID: "live-test-acct", APIKeyID: "live-test-key"}, nil
}

type liveAccountingStub struct{}

func (liveAccountingStub) CreateReservation(_ context.Context, _ audio.ReservationInput) (string, error) {
	return "live-test-reservation", nil
}

func (liveAccountingStub) FinalizeReservation(_ context.Context, _ audio.FinalizeInput) error {
	return nil
}

func (liveAccountingStub) ReleaseReservation(_ context.Context, _, _, _ string) error {
	return nil
}

func liveAudioHandler(t *testing.T, providerModel string) *audio.Handler {
	t.Helper()
	groqKey := os.Getenv("GROQ_API_KEY")
	if groqKey == "" {
		t.Fatal("GROQ_API_KEY not set; required when HIVE_AUDIO_LIVE_TEST=1")
	}
	// Groq's API is OpenAI-protocol compatible at the same relative paths
	// (/audio/speech, /audio/transcriptions) that Hive's handler forwards
	// to, so pointing litellmBaseURL straight at Groq exercises the exact
	// same request-construction and response-passthrough code the handler
	// uses against LiteLLM in production.
	return audio.NewHandler(liveAuthStub{}, liveRoutingStub{providerModel: providerModel}, liveAccountingStub{}, "https://api.groq.com/openai/v1", groqKey)
}

// TestLiveVoiceRoundTrip speaks a known sentence through a real TTS provider,
// transcribes the resulting audio through a real STT provider, and asserts
// the recovered text matches what was spoken.
func TestLiveVoiceRoundTrip(t *testing.T) {
	if os.Getenv("HIVE_AUDIO_LIVE_TEST") != "1" {
		t.Skip("HIVE_AUDIO_LIVE_TEST not set; skipping live voice provider round trip")
	}

	const sentence = "The quick brown fox jumps over the lazy dog"

	// Leg 1: text to speech via the real Handler + real Groq TTS model.
	speechHandler := liveAudioHandler(t, "canopylabs/orpheus-v1-english")
	// response_format is required here: Groq's Orpheus TTS backend rejects
	// the request with "response_format must be one of [wav]" when the
	// field is omitted, unlike OpenAI's own API, which defaults to mp3.
	// Hive's speech handler passes response_format straight through without
	// filling in a provider-appropriate default, so an OpenAI client that
	// omits it (common — the field is optional in OpenAI's own contract)
	// gets a 500 from this route today. Filed as a finding, not fixed here:
	// out of scope for this test-coverage pass.
	speechBody, err := json.Marshal(map[string]string{
		"model":           "hive-tts",
		"input":           sentence,
		"voice":           "troy",
		"response_format": "wav",
	})
	if err != nil {
		t.Fatalf("marshal speech request: %v", err)
	}
	speechReq := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", bytes.NewReader(speechBody))
	speechReq.Header.Set("Content-Type", "application/json")
	speechRec := httptest.NewRecorder()
	speechHandler.ServeHTTP(speechRec, speechReq)

	if speechRec.Code != http.StatusOK {
		t.Fatalf("speech synthesis failed: status=%d body=%s", speechRec.Code, speechRec.Body.String())
	}
	audioBytes := speechRec.Body.Bytes()
	if len(audioBytes) == 0 {
		t.Fatal("speech synthesis returned an empty body")
	}

	// Leg 2: feed the synthesized audio into the real Handler + real Groq
	// Whisper STT model.
	transcribeHandler := liveAudioHandler(t, "whisper-large-v3")
	var multipartBody bytes.Buffer
	mw := multipart.NewWriter(&multipartBody)
	if err := mw.WriteField("model", "hive-stt"); err != nil {
		t.Fatalf("write model field: %v", err)
	}
	if err := mw.WriteField("language", "en"); err != nil {
		t.Fatalf("write language field: %v", err)
	}
	// speech.wav, not .mp3: the TTS leg above requested response_format=wav,
	// and the STT backend infers container format from the filename
	// extension when decoding, so a mismatched extension here silently
	// truncates the transcription instead of failing loudly.
	fw, err := mw.CreateFormFile("file", "speech.wav")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(audioBytes); err != nil {
		t.Fatalf("write audio to multipart: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	transcribeReq := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &multipartBody)
	transcribeReq.Header.Set("Content-Type", mw.FormDataContentType())
	transcribeRec := httptest.NewRecorder()
	transcribeHandler.ServeHTTP(transcribeRec, transcribeReq)

	if transcribeRec.Code != http.StatusOK {
		t.Fatalf("transcription failed: status=%d body=%s", transcribeRec.Code, transcribeRec.Body.String())
	}

	// Response shape: OpenAI's CreateTranscriptionResponseJson contract
	// (packages/openai-contract) requires a "text" field. Assert the shape,
	// not just a 200.
	var got audio.TranscriptionResponse
	if err := json.Unmarshal(transcribeRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("transcription response is not valid JSON matching the OpenAI contract: %v (body=%s)", err, transcribeRec.Body.String())
	}
	if strings.TrimSpace(got.Text) == "" {
		t.Fatal("transcription response has an empty text field")
	}

	// Correctness, not just plumbing: the transcribed text must actually
	// recover the words that were spoken.
	gotLower := strings.ToLower(got.Text)
	for _, word := range []string{"quick", "brown", "fox", "lazy", "dog"} {
		if !strings.Contains(gotLower, word) {
			t.Errorf("transcription %q missing expected word %q from spoken sentence %q", got.Text, word, sentence)
		}
	}
}
