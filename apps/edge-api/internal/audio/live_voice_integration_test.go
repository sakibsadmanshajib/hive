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
// IMPORTANT — this test points at Groq directly rather than through
// LiteLLM, unlike Hive's real deployment, to isolate audio.Handler's own
// dispatch, multipart handling, and response passthrough from anything
// upstream of it. Hive's provider-agnostic design means the handler code
// under test here is identical either way; only the base URL changes.
// TestLiveVoiceRoundTripThroughLiteLLM below is this test's production
// twin, run against a real LiteLLM proxy instead.
//
// Follow-up finding (resolved): while writing this test, live verification
// first appeared to show LiteLLM-specific corruption -- audio round-tripped
// through LiteLLM's route-groq-tts / route-groq-stt intermittently came back
// as HTTP 200 with a correctly-shaped WAV file that was entirely silent
// (all-zero PCM samples), which Whisper then (correctly) transcribed as
// near-garbage (" you") on the STT leg. Wider live testing traced this to
// Groq's Orpheus TTS backend itself, not LiteLLM or Hive's own code: calling
// Groq's API directly, with no LiteLLM or Hive code in between, reproduces
// the exact same intermittent silent-WAV response (confirmed via repeated
// live curl requests against api.groq.com, same request, same ~1-in-4 to
// ~1-in-5 failure rate, byte-identical corrupted payload each time it
// occurred). The STT leg was never independently broken; it was always
// correctly transcribing whatever audio the TTS leg actually produced.
// handleSpeech (handler.go) now retries internally when it detects a
// silent-WAV response and only returns 200 once it has verified non-silent
// audio, so this is fixed at the point Hive controls even though the root
// cause is upstream and outside Hive's (or LiteLLM's) control.
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

// liveAudioHandlerLiteLLM builds the same production audio.Handler as
// liveAudioHandler above, but points litellmBaseURL at a real LiteLLM proxy
// instance instead of straight at Groq. This is the production topology
// (deploy/litellm/config.yaml's route-groq-tts / route-groq-stt), unlike
// TestLiveVoiceRoundTrip above which bypasses LiteLLM entirely.
func liveAudioHandlerLiteLLM(t *testing.T, litellmModelName string) *audio.Handler {
	t.Helper()
	base := os.Getenv("LITELLM_TEST_BASE_URL")
	if base == "" {
		base = "http://localhost:4000"
	}
	masterKey := os.Getenv("LITELLM_TEST_MASTER_KEY")
	if masterKey == "" {
		masterKey = "litellm-dev-key"
	}
	return audio.NewHandler(liveAuthStub{}, liveRoutingStub{providerModel: litellmModelName}, liveAccountingStub{}, base, masterKey)
}

// TestLiveVoiceRoundTripThroughLiteLLM is TestLiveVoiceRoundTrip's production
// twin: it speaks the same known sentence through the real Handler pointed at
// a real LiteLLM proxy (deploy/litellm/config.yaml's route-groq-tts /
// route-groq-stt, which both forward to Groq), instead of hitting Groq
// directly. This is the actual topology every real Hive voice request goes
// through, so a pass here (and not just in TestLiveVoiceRoundTrip) is the
// only live proof that production /v1/audio traffic is not corrupted.
//
// Prerequisites:
//   - A running LiteLLM proxy with route-groq-tts / route-groq-stt configured
//     and a real GROQ_API_KEY behind them (see deploy/litellm/config.yaml).
//   - LITELLM_TEST_BASE_URL pointed at that proxy (default http://localhost:4000).
//   - LITELLM_TEST_MASTER_KEY matching its LITELLM_MASTER_KEY (default
//     litellm-dev-key, matching deploy/docker/docker-compose.yml's dev default).
//   - HIVE_AUDIO_LITELLM_LIVE_TEST=1 to opt in.
func TestLiveVoiceRoundTripThroughLiteLLM(t *testing.T) {
	if os.Getenv("HIVE_AUDIO_LITELLM_LIVE_TEST") != "1" {
		t.Skip("HIVE_AUDIO_LITELLM_LIVE_TEST not set; skipping live voice round trip through LiteLLM")
	}

	const sentence = "The quick brown fox jumps over the lazy dog"

	// Leg 1: text to speech via the real Handler, through LiteLLM's
	// route-groq-tts, to Groq's Orpheus TTS backend.
	speechHandler := liveAudioHandlerLiteLLM(t, "route-groq-tts")
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
		t.Fatalf("speech synthesis through LiteLLM failed: status=%d body=%s", speechRec.Code, speechRec.Body.String())
	}
	audioBytes := speechRec.Body.Bytes()
	if len(audioBytes) == 0 {
		t.Fatal("speech synthesis through LiteLLM returned an empty body")
	}

	// PCM content check, not just byte count: a WAV file with a correct
	// header and length but all-zero sample data is exactly the confirmed
	// live defect this test guards against (see handleSpeech's silent-WAV
	// retry loop in handler.go) -- Groq's Orpheus TTS backend intermittently
	// returns HTTP 200 with silence instead of the requested speech, on a
	// meaningful fraction of requests, reproduced identically calling Groq
	// directly and through LiteLLM. handleSpeech retries internally and
	// only returns 200 once it has verified non-silent audio (or gives up
	// with a 503 after exhausting retries), so this assertion should never
	// fail in practice; it stays here as a regression guard against that
	// retry logic being weakened or removed.
	sampleData := audioBytes
	if len(sampleData) > 44 {
		sampleData = sampleData[44:]
	}
	allZero := true
	for _, b := range sampleData {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatal("speech synthesis through LiteLLM returned WAV sample data that is entirely zero (silence) — audio corrupted in transit")
	}

	// Leg 2: feed the synthesized audio into the real Handler, through
	// LiteLLM's route-groq-stt, to Groq's Whisper backend.
	transcribeHandler := liveAudioHandlerLiteLLM(t, "route-groq-stt")
	var multipartBody bytes.Buffer
	mw := multipart.NewWriter(&multipartBody)
	if err := mw.WriteField("model", "hive-stt"); err != nil {
		t.Fatalf("write model field: %v", err)
	}
	if err := mw.WriteField("language", "en"); err != nil {
		t.Fatalf("write language field: %v", err)
	}
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
		t.Fatalf("transcription through LiteLLM failed: status=%d body=%s", transcribeRec.Code, transcribeRec.Body.String())
	}

	var got audio.TranscriptionResponse
	if err := json.Unmarshal(transcribeRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("transcription response through LiteLLM is not valid JSON matching the OpenAI contract: %v (body=%s)", err, transcribeRec.Body.String())
	}
	if strings.TrimSpace(got.Text) == "" {
		t.Fatal("transcription response through LiteLLM has an empty text field")
	}

	gotLower := strings.ToLower(got.Text)
	for _, word := range []string{"quick", "brown", "fox", "lazy", "dog"} {
		if !strings.Contains(gotLower, word) {
			t.Errorf("transcription through LiteLLM %q missing expected word %q from spoken sentence %q", got.Text, word, sentence)
		}
	}
}
