package audio_test

// Issue #627: audio billing must be derived from the model catalog, not from a
// flat literal. Every assertion in this file computes the expected charge
// independently from the catalog row the routing layer hands the handler, then
// requires the handler's charge to land within one credit of it. That bound,
// not "the handler reads the catalog", is what these tests exist to hold: a
// handler that reads the catalog and then applies the wrong unit conversion
// would satisfy the looser claim and still misprice by orders of magnitude.
//
// Quantities here are deliberately large (5000 characters, 1800 seconds). The
// floor-at-one-credit rule makes any small-quantity assertion pass for the
// wrong reason.

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/audio"
)

// The two catalog prices under test, in credits per million metered units,
// as written by supabase/migrations/20260801_13_alias_price_unit.sql in the
// PRE-RESCALE credit unit (migration 20260823_40 later multiplied every
// stored price by 10,000; these fixtures are hand-built routes for the
// arithmetic contract, so their absolute size is arbitrary). Derived from the
// provider's published rate (Groq, fetched 2026-08-01) times the 1.4 margin
// times the then-current CreditsPerUSD:
//
//	TTS  $22.00 per 1M characters      -> 22.00      * 1.4 * 100000 = 3080000
//	STT  $0.111 per hour transcribed   -> 30.8333... * 1.4 * 100000 = 4316667
const (
	ttsCreditsPerMillionCharacters = 3_080_000
	sttCreditsPerMillionSeconds    = 4_316_667
)

// catalogCharge is the reference implementation of the charge these tests
// hold the handler to: quantity times credits-per-million-units, divided by a
// million, rounded half up. Written out longhand here on purpose -- reusing
// the handler's own helper would make the assertion tautological.
func catalogCharge(quantity, creditsPerMillion int64) int64 {
	return (quantity*creditsPerMillion + 500_000) / 1_000_000
}

func assertWithinOneCredit(t *testing.T, label string, got, want int64) {
	t.Helper()

	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	if diff > 1 {
		t.Fatalf("%s: charged %d credits, catalog row says %d (allowed drift is 1 credit)", label, got, want)
	}
}

// --- /v1/audio/speech: characters ---

func TestSpeechChargesCatalogPricePerCharacter(t *testing.T) {
	mock := newMockLiteLLMAudio([]byte{0xFF, 0xFB, 0x90, 0x00}, 200, "audio/mpeg")
	defer mock.Close()

	const characters = 5000
	routing := &mockAudioRouting{unitPriceCredits: ttsCreditsPerMillionCharacters, priceUnit: "characters"}
	acc := &mockAudioAccounting{reservationID: "res-speech-price"}
	h := audio.NewHandler(&mockAudioAuthorizer{accountID: "acct-test", apiKeyID: "key-test"}, routing, acc, mock.server.URL, "test-key")

	body := fmt.Sprintf(`{"model":"hive-tts","input":%q,"voice":"alloy"}`, strings.Repeat("a", characters))
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	want := catalogCharge(characters, ttsCreditsPerMillionCharacters)
	if want != 15400 {
		t.Fatalf("test arithmetic drifted: expected 15400 credits for %d characters, computed %d", characters, want)
	}
	assertWithinOneCredit(t, "finalized charge", acc.lastActualCredits, want)
	// The hold is exact here too: the character count is known before dispatch,
	// so there is no reason for the estimate to differ from the charge.
	assertWithinOneCredit(t, "reserved estimate", acc.lastEstimatedCredits, want)
}

func TestSpeechChargeScalesWithInputLength(t *testing.T) {
	charge := func(characters int) int64 {
		mock := newMockLiteLLMAudio([]byte{0xFF, 0xFB}, 200, "audio/mpeg")
		defer mock.Close()

		routing := &mockAudioRouting{unitPriceCredits: ttsCreditsPerMillionCharacters, priceUnit: "characters"}
		acc := &mockAudioAccounting{reservationID: "res-speech-scale"}
		h := audio.NewHandler(&mockAudioAuthorizer{accountID: "acct-test", apiKeyID: "key-test"}, routing, acc, mock.server.URL, "test-key")

		body := fmt.Sprintf(`{"model":"hive-tts","input":%q,"voice":"alloy"}`, strings.Repeat("b", characters))
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for %d characters, got %d: %s", characters, w.Code, w.Body.String())
		}
		return acc.lastActualCredits
	}

	small, large := charge(2_000), charge(20_000)
	if large <= small {
		t.Fatalf("charge must scale with synthesized characters: 2000 chars charged %d, 20000 chars charged %d", small, large)
	}
	assertWithinOneCredit(t, "10x input", large, 10*small)
}

// A price row whose unit is not the unit this endpoint meters cannot be
// converted into a charge. Refusing is the only fail-closed answer: serving
// the request would mean either inventing a conversion or serving it free.
func TestSpeechRefusesPriceUnitItCannotMeter(t *testing.T) {
	mock := newMockLiteLLMAudio([]byte{0xFF, 0xFB}, 200, "audio/mpeg")
	defer mock.Close()

	routing := &mockAudioRouting{unitPriceCredits: 21_000, priceUnit: "tokens"}
	acc := &mockAudioAccounting{reservationID: "res-speech-unit-mismatch"}
	h := audio.NewHandler(&mockAudioAuthorizer{accountID: "acct-test", apiKeyID: "key-test"}, routing, acc, mock.server.URL, "test-key")

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech",
		strings.NewReader(`{"model":"hive-default","input":"hello","voice":"alloy"}`))
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("expected a refusal for a token-priced alias on /v1/audio/speech, got 200: %s", w.Body.String())
	}
	if acc.createCalled {
		t.Error("expected no reservation for an alias this endpoint cannot price")
	}
	assertAudioMessageProviderBlind(t, decodeAudioError(t, w).Error.Message)
}

// --- /v1/audio/transcriptions and /v1/audio/translations: seconds ---

func postAudioMultipart(t *testing.T, h *audio.Handler, path, model, responseFormat string) *httptest.ResponseRecorder {
	t.Helper()

	body, contentType := multipartAudioBody(t, model, responseFormat)
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// multipartAudioBody builds one audio upload form, returning the body and its
// Content-Type.
func multipartAudioBody(t *testing.T, model, responseFormat string) (*bytes.Buffer, string) {
	t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("model", model); err != nil {
		t.Fatalf("write model field: %v", err)
	}
	if responseFormat != "" {
		if err := mw.WriteField("response_format", responseFormat); err != nil {
			t.Fatalf("write response_format field: %v", err)
		}
	}
	fw, err := mw.CreateFormFile("file", "audio.wav")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte("fake-audio-bytes")); err != nil {
		t.Fatalf("write audio bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

func buildPricedAudioHandler(litellmBaseURL string, unitPrice int64, unit string) (*audio.Handler, *mockAudioAccounting, *mockAudioRouting) {
	routing := &mockAudioRouting{unitPriceCredits: unitPrice, priceUnit: unit}
	acc := &mockAudioAccounting{reservationID: "res-priced"}
	h := audio.NewHandler(&mockAudioAuthorizer{accountID: "acct-test", apiKeyID: "key-test"}, routing, acc, litellmBaseURL, "test-key")
	return h, acc, routing
}

func TestTranscriptionChargesCatalogPricePerSecond(t *testing.T) {
	const seconds = 1800
	mock := newMockLiteLLMAudio([]byte(fmt.Sprintf(`{"text":"a long meeting","duration":%d.0}`, seconds)), 200, "application/json")
	defer mock.Close()

	h, acc, _ := buildPricedAudioHandler(mock.server.URL, sttCreditsPerMillionSeconds, "seconds")
	w := postAudioMultipart(t, h, "/v1/audio/transcriptions", "hive-stt", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	want := catalogCharge(seconds, sttCreditsPerMillionSeconds)
	if want != 7770 {
		t.Fatalf("test arithmetic drifted: expected 7770 credits for %d seconds, computed %d", seconds, want)
	}
	assertWithinOneCredit(t, "finalized charge", acc.lastActualCredits, want)
}

func TestTranslationChargesCatalogPricePerSecond(t *testing.T) {
	const seconds = 600
	mock := newMockLiteLLMAudio([]byte(fmt.Sprintf(`{"text":"bonjour","duration":%d.0}`, seconds)), 200, "application/json")
	defer mock.Close()

	h, acc, _ := buildPricedAudioHandler(mock.server.URL, sttCreditsPerMillionSeconds, "seconds")
	w := postAudioMultipart(t, h, "/v1/audio/translations", "hive-stt", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertWithinOneCredit(t, "finalized charge", acc.lastActualCredits, catalogCharge(seconds, sttCreditsPerMillionSeconds))
}

// The upstream must be asked for a response shape that carries the duration.
// Without this the default response_format (json) reports text only, the
// handler has no metered quantity, and every transcription would have to be
// refused.
func TestTranscriptionAsksUpstreamForDurationCarryingFormat(t *testing.T) {
	mock := newMockLiteLLMAudio([]byte(`{"text":"hello","duration":120.0,"segments":[]}`), 200, "application/json")
	defer mock.Close()

	h, _, _ := buildPricedAudioHandler(mock.server.URL, sttCreditsPerMillionSeconds, "seconds")
	w := postAudioMultipart(t, h, "/v1/audio/transcriptions", "hive-stt", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(string(mock.lastBody), "verbose_json") {
		t.Fatalf("expected the upstream request to ask for verbose_json so a duration is reported, body was: %s", mock.lastBody)
	}
	// A caller that asked for the default json shape must still get the json
	// shape back, not the verbose one this handler asked upstream for.
	if strings.Contains(w.Body.String(), "segments") {
		t.Fatalf("verbose fields leaked into a default-format response: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"text":"hello"`) {
		t.Fatalf("expected the transcribed text in the response, got: %s", w.Body.String())
	}
}

// A 2xx with no duration anywhere is unpriceable. It must be refused and the
// hold released, never finalized at a guess and never served free (D-034).
func TestTranscriptionRefusesUnpriceableResponse(t *testing.T) {
	mock := newMockLiteLLMAudio([]byte(`{"text":"no duration reported"}`), 200, "application/json")
	defer mock.Close()

	h, acc, _ := buildPricedAudioHandler(mock.server.URL, sttCreditsPerMillionSeconds, "seconds")
	w := postAudioMultipart(t, h, "/v1/audio/transcriptions", "hive-stt", "")

	if w.Code == http.StatusOK {
		t.Fatalf("expected a refusal when no duration is reported, got 200: %s", w.Body.String())
	}
	if acc.finalizeCalled {
		t.Error("expected no finalize for a request that could not be priced")
	}
	if !acc.releaseCalled {
		t.Error("expected the hold to be released when the request is refused")
	}
	assertAudioMessageProviderBlind(t, decodeAudioError(t, w).Error.Message)
}

// Groq bills a minimum of 10 seconds per transcription request
// (https://groq.com/pricing, "Audio is billed at a minimum of 10s per
// request"). Charging the reported 1.2 seconds would serve the request below
// cost.
func TestTranscriptionAppliesProviderMinimumBillableDuration(t *testing.T) {
	mock := newMockLiteLLMAudio([]byte(`{"text":"hi","duration":1.2}`), 200, "application/json")
	defer mock.Close()

	h, acc, _ := buildPricedAudioHandler(mock.server.URL, sttCreditsPerMillionSeconds, "seconds")
	w := postAudioMultipart(t, h, "/v1/audio/transcriptions", "hive-stt", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertWithinOneCredit(t, "finalized charge", acc.lastActualCredits, catalogCharge(10, sttCreditsPerMillionSeconds))
}

func TestTranscriptionRefusesPriceUnitItCannotMeter(t *testing.T) {
	mock := newMockLiteLLMAudio([]byte(`{"text":"unused","duration":30.0}`), 200, "application/json")
	defer mock.Close()

	h, acc, _ := buildPricedAudioHandler(mock.server.URL, 3_080_000, "characters")
	w := postAudioMultipart(t, h, "/v1/audio/transcriptions", "hive-tts", "")

	if w.Code == http.StatusOK {
		t.Fatalf("expected a refusal for a character-priced alias on /v1/audio/transcriptions, got 200: %s", w.Body.String())
	}
	if acc.createCalled {
		t.Error("expected no reservation for an alias this endpoint cannot price")
	}
}

// A caller asking for srt or vtt gets those formats verbatim; the duration
// comes from the last cue timestamp, so the request is still priced from the
// catalog rather than refused or flat-charged.
func TestTranscriptionPricesSubtitleFormatsFromCueTimestamps(t *testing.T) {
	srt := "1\n00:00:00,000 --> 00:00:12,500\nhello\n\n2\n00:19:58,000 --> 00:20:00,000\nthe end\n"
	mock := newMockLiteLLMAudio([]byte(srt), 200, "text/plain")
	defer mock.Close()

	h, acc, _ := buildPricedAudioHandler(mock.server.URL, sttCreditsPerMillionSeconds, "seconds")
	w := postAudioMultipart(t, h, "/v1/audio/transcriptions", "hive-stt", "srt")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != srt {
		t.Fatalf("expected the subtitle body passed through verbatim, got: %s", w.Body.String())
	}
	assertWithinOneCredit(t, "finalized charge", acc.lastActualCredits, catalogCharge(1200, sttCreditsPerMillionSeconds))
}

// The local sovereign STT path (WithSTT) went through none of this: it never
// consulted routing at all and charged a flat literal.
func TestLocalSTTChargesCatalogPricePerSecond(t *testing.T) {
	const seconds = 900
	sttBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"text":"local transcription","duration":%d.0}`, seconds)
	}))
	defer sttBackend.Close()

	routing := &mockAudioRouting{unitPriceCredits: sttCreditsPerMillionSeconds, priceUnit: "seconds"}
	acc := &mockAudioAccounting{reservationID: "res-local-stt"}
	h := audio.NewHandler(&mockAudioAuthorizer{accountID: "acct-test", apiKeyID: "key-test"}, routing, acc, "http://unused.example.com", "test-key")
	attachSTT(h, sttBackend.URL)

	w := postAudioMultipart(t, h, "/v1/audio/transcriptions", "hive-stt", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertWithinOneCredit(t, "finalized charge", acc.lastActualCredits, catalogCharge(seconds, sttCreditsPerMillionSeconds))
	if routing.lastAliasID != "hive-stt" {
		t.Fatalf("expected the local path to price the alias the caller named, routing saw %q", routing.lastAliasID)
	}
}

// No customer-facing audio surface may carry a provider name, whatever the
// charge turns out to be.
func TestAudioResponsesCarryNoProviderLanguage(t *testing.T) {
	mock := newMockLiteLLMAudio([]byte(`{"text":"hello","duration":45.0}`), 200, "application/json")
	defer mock.Close()

	h, _, _ := buildPricedAudioHandler(mock.server.URL, sttCreditsPerMillionSeconds, "seconds")
	w := postAudioMultipart(t, h, "/v1/audio/transcriptions", "hive-stt", "")

	lower := strings.ToLower(w.Body.String())
	for _, forbidden := range []string{"groq", "openrouter", "litellm"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("customer-visible transcription response contains %q: %s", forbidden, w.Body.String())
		}
	}
}

// --- Issue #680: a 2xx with no top-level duration is priced from segment ends ---
//
// verbose_json carries per-segment start and end times whether or not the
// upstream reports a top-level duration. Every segment end is bounded by the
// real audio length, so the maximum of them can never charge for more audio
// than the caller sent, and it is the same derivation lastCueSeconds already
// applies to subtitle bodies. Before this fallback, one missing field refused
// every transcription with 502, the only total outage mode on this money path.

func TestTranscriptionPricesFromSegmentEndsWhenDurationAbsent(t *testing.T) {
	const seconds = 1800
	// Deliberately out of order, so a handler that took the last segment
	// rather than the maximum would charge 120 seconds and fail here.
	body := fmt.Sprintf(
		`{"text":"a long meeting","segments":[{"id":0,"start":0.0,"end":42.5},{"id":1,"start":42.5,"end":%d.0},{"id":2,"start":100.0,"end":120.0}]}`,
		seconds)
	mock := newMockLiteLLMAudio([]byte(body), 200, "application/json")
	defer mock.Close()

	h, acc, _ := buildPricedAudioHandler(mock.server.URL, sttCreditsPerMillionSeconds, "seconds")
	w := postAudioMultipart(t, h, "/v1/audio/transcriptions", "hive-stt", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for a response priceable from its segments, got %d: %s", w.Code, w.Body.String())
	}
	if !acc.finalizeCalled {
		t.Fatal("expected the reservation to be finalized for a transcription priced from its segments")
	}
	assertWithinOneCredit(t, "finalized charge", acc.lastActualCredits, catalogCharge(seconds, sttCreditsPerMillionSeconds))
}

// The local sovereign STT path reads the same body through its own call site,
// so it needs the fallback too.
func TestLocalSTTPricesFromSegmentEndsWhenDurationAbsent(t *testing.T) {
	const seconds = 900
	sttBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"text":"local transcription","segments":[{"start":0.0,"end":12.0},{"start":12.0,"end":%d.0}]}`, seconds)
	}))
	defer sttBackend.Close()

	routing := &mockAudioRouting{unitPriceCredits: sttCreditsPerMillionSeconds, priceUnit: "seconds"}
	acc := &mockAudioAccounting{reservationID: "res-local-stt-segments"}
	h := audio.NewHandler(&mockAudioAuthorizer{accountID: "acct-test", apiKeyID: "key-test"}, routing, acc, "http://unused.example.com", "test-key")
	attachSTT(h, sttBackend.URL)

	w := postAudioMultipart(t, h, "/v1/audio/transcriptions", "hive-stt", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for a local transcription priceable from its segments, got %d: %s", w.Code, w.Body.String())
	}
	assertWithinOneCredit(t, "finalized charge", acc.lastActualCredits, catalogCharge(seconds, sttCreditsPerMillionSeconds))
}

// A fractional segment end rounds up to the whole second, exactly as a
// reported top-level duration does. Rounding down would serve part of the
// audio free.
func TestTranscriptionRoundsSegmentDerivedDurationUp(t *testing.T) {
	mock := newMockLiteLLMAudio([]byte(`{"text":"hi","segments":[{"start":0.0,"end":1799.2}]}`), 200, "application/json")
	defer mock.Close()

	h, acc, _ := buildPricedAudioHandler(mock.server.URL, sttCreditsPerMillionSeconds, "seconds")
	w := postAudioMultipart(t, h, "/v1/audio/transcriptions", "hive-stt", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertWithinOneCredit(t, "finalized charge", acc.lastActualCredits, catalogCharge(1800, sttCreditsPerMillionSeconds))
}

// The bound on this change: a response that already carried a top-level
// duration must be charged exactly what it was charged before, even when its
// segments run longer. The top-level duration wins outright, and the segments
// are a fallback rather than a maximum taken across both.
func TestTranscriptionTopLevelDurationWinsOverLongerSegments(t *testing.T) {
	const reported = 120
	body := fmt.Sprintf(
		`{"text":"short clip","duration":%d.0,"segments":[{"start":0.0,"end":1800.0}]}`, reported)
	mock := newMockLiteLLMAudio([]byte(body), 200, "application/json")
	defer mock.Close()

	h, acc, _ := buildPricedAudioHandler(mock.server.URL, sttCreditsPerMillionSeconds, "seconds")
	w := postAudioMultipart(t, h, "/v1/audio/transcriptions", "hive-stt", "")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertWithinOneCredit(t, "finalized charge", acc.lastActualCredits, catalogCharge(reported, sttCreditsPerMillionSeconds))
}

// Neither present: the fallback must not manufacture a zero second duration,
// which the ten second provider minimum would then turn into a real charge for
// audio nobody can account for. With no top-level duration and no usable
// segment end the response stays unpriceable, so it is refused, the hold is
// released, and nothing is charged (D-034).
func TestTranscriptionRefusesWhenNeitherDurationNorSegmentsUsable(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"no segments key at all", `{"text":"x"}`},
		{"empty segments array", `{"text":"x","segments":[]}`},
		{"segments carry no end field", `{"text":"x","segments":[{"start":0.0},{"start":1.0}]}`},
		{"segments carry a null end", `{"text":"x","segments":[{"start":0.0,"end":null}]}`},
		{"segment ends are not positive", `{"text":"x","segments":[{"start":0.0,"end":0.0},{"start":0.0,"end":-3.0}]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newMockLiteLLMAudio([]byte(tc.body), 200, "application/json")
			defer mock.Close()

			h, acc, _ := buildPricedAudioHandler(mock.server.URL, sttCreditsPerMillionSeconds, "seconds")
			w := postAudioMultipart(t, h, "/v1/audio/transcriptions", "hive-stt", "")

			if w.Code == http.StatusOK {
				t.Fatalf("expected a refusal when nothing in the body reports a duration, got 200: %s", w.Body.String())
			}
			if acc.finalizeCalled {
				t.Errorf("expected no finalize for a request that could not be priced, charged %d credits", acc.lastActualCredits)
			}
			if !acc.releaseCalled {
				t.Error("expected the hold to be released when the request is refused")
			}
			assertAudioMessageProviderBlind(t, decodeAudioError(t, w).Error.Message)
		})
	}
}
