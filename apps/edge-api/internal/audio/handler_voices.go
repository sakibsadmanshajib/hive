package audio

import (
	"encoding/json"
	"net/http"
	"strings"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// Voice catalog for the hive-tts alias (groq/orpheus-v1-english). These are
// the only voice ids the upstream model accepts, and the roster this gateway
// advertises. The OpenAI stock names are accepted as well: resolveVoice below
// translates them onto this roster (#1318). They are deliberately not listed
// here, because they are aliases of these six voices rather than six more
// distinct voices, and listing them would fill every client voice dropdown
// with duplicates.
//
// ponytail: static slice rather than a config knob or DB table. The list
// changes only when the provider swaps its voice roster, at which point this
// file changes with it; nothing needs to configure six fixed strings at boot.
var orpheusVoices = []voiceEntry{
	{"autumn", "Autumn"},
	{"diana", "Diana"},
	{"hannah", "Hannah"},
	{"austin", "Austin"},
	{"daniel", "Daniel"},
	{"troy", "Troy"},
}

type voiceEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// VoicesHandler serves GET /v1/audio/voices, the endpoint Open WebUI's
// get_available_voices fetches when audio.tts.engine is openai and the base
// URL is not api.openai.com (routers/audio.py). It sends no Authorization
// header of any kind, so this route is deliberately registered without the
// hk_-key authorizer or the tenant voice gate: there is no credential to
// validate and nothing here is per-account data, only the six public voice
// names the provider publishes. Gating it would silently break the Settings >
// Audio voice dropdowns back to Open WebUI's hardcoded alloy-style fallback,
// which is the exact defect (#996) this exists to prevent.
func VoicesHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			apierrors.WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Method not allowed", nil)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"voices": orpheusVoices})
	}
}

// openAIStockVoices maps the OpenAI published voice names onto the roster
// above.
//
// Hive presents an OpenAI-compatible surface and every example in every OpenAI
// SDK sends the voice name alloy, so forwarding the name verbatim made
// /v1/audio/speech uncallable by an unmodified SDK: the upstream refused it
// and the caller received a 500 (#1318, #1285, #996). Translating is the
// friendlier half of the compatibility promise and it costs the caller
// nothing. Refusing with a list naming the roster would have been honest too,
// but it leaves every stock SDK example broken.
//
// The pairing is by rough timbre (six targets, eleven sources, so some share a
// target). It is arbitrary but STABLE: a caller asking for the same OpenAI
// voice twice must never get a different voice the second time. Nothing here
// claims the synthesized voice sounds like the OpenAI one.
//
// ponytail: a map beside the roster it maps onto, not catalog data. Issue
// #1318 asks for the voice list to live next to the route, and it should the
// day a second TTS route with a different roster exists. Today there is
// exactly one TTS alias, and a table for eleven fixed strings is a schema to
// keep in sync with no second reader.
var openAIStockVoices = map[string]string{
	"alloy":   "autumn",
	"ash":     "austin",
	"ballad":  "daniel",
	"coral":   "diana",
	"echo":    "troy",
	"fable":   "hannah",
	"nova":    "autumn",
	"onyx":    "daniel",
	"sage":    "hannah",
	"shimmer": "diana",
	"verse":   "troy",
}

// resolveVoice maps a caller-supplied voice name onto the voice id the
// upstream accepts. It reports false for a name in neither set, which the
// speech handler answers with a 400 naming the supported voices rather than
// forwarding a name the upstream is going to reject.
//
// Case and surrounding whitespace are normalized. An OpenAI SDK always sends
// lowercase; a hand-written client sending Alloy made no meaningful mistake.
func resolveVoice(requested string) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(requested))
	if name == "" {
		return "", false
	}
	for _, v := range orpheusVoices {
		if v.ID == name {
			return v.ID, true
		}
	}
	mapped, ok := openAIStockVoices[name]
	return mapped, ok
}

// supportedVoiceNames is the comma-separated roster the refusal message names,
// so a caller who sent something unrecognized can act on the error without
// reading documentation. It is built from orpheusVoices, so the message cannot
// drift from what GET /v1/audio/voices advertises or from what resolveVoice
// accepts.
func supportedVoiceNames() string {
	names := make([]string, 0, len(orpheusVoices))
	for _, v := range orpheusVoices {
		names = append(names, v.ID)
	}
	return strings.Join(names, ", ")
}

// speechResponseFormats is the audio container the hive-tts route can actually
// produce. Groq's Orpheus endpoint answers "response_format must be one of
// [wav]" to anything else, and LiteLLM relays that refusal as a 500, so the
// caller saw a sanitized gateway failure and the real sentence stayed in the
// LiteLLM log (#1381).
//
// ponytail: one static roster for the one TTS route this gateway has, matching
// how orpheusVoices above handles the same question for voices. If a second
// TTS route with a different container is ever added, this moves onto the
// route record next to the voice roster, both at once rather than one now.
var speechResponseFormats = []string{"wav"}

// defaultSpeechResponseFormat is what the handler asks the upstream for when
// the caller says nothing. It cannot be left to the upstream: the OpenAI SDK
// omits response_format, the OpenAI default is mp3, and the route refuses mp3.
func defaultSpeechResponseFormat() string {
	return speechResponseFormats[0]
}

// resolveSpeechResponseFormat maps what the caller asked for onto what the
// route can produce. An empty request resolves to the route default; anything
// the route cannot produce is refused rather than silently rewritten, because
// a caller who explicitly asked for mp3 and received wav would have been given
// bytes it cannot decode with no indication why.
func resolveSpeechResponseFormat(requested string) (string, bool) {
	format := strings.ToLower(strings.TrimSpace(requested))
	if format == "" {
		return defaultSpeechResponseFormat(), true
	}
	for _, supported := range speechResponseFormats {
		if format == supported {
			return supported, true
		}
	}
	return "", false
}

// supportedSpeechFormatNames is the comma-separated roster the refusal message
// names, built from speechResponseFormats so the message cannot drift from
// what resolveSpeechResponseFormat accepts.
func supportedSpeechFormatNames() string {
	return strings.Join(speechResponseFormats, ", ")
}
