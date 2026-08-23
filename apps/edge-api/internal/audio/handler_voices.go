package audio

import (
	"encoding/json"
	"net/http"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// Voice catalog for the hive-tts alias (groq/orpheus-v1-english). These are
// the only voice ids the upstream model accepts; OpenAI's stock names (alloy,
// echo, ...) are rejected with an upstream 400 that today surfaces as a
// sanitized 500 (#996), so every client we control must offer exactly these.
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
