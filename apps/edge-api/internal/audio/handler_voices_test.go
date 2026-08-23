package audio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVoicesHandlerGetReturnsProviderRoster(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	VoicesHandler()(rec, httptest.NewRequest(http.MethodGet, "/v1/audio/voices", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/audio/voices: got %d want 200, body %q", rec.Code, rec.Body.String())
	}
	var body struct {
		Voices []voiceEntry `json:"voices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if len(body.Voices) != len(orpheusVoices) {
		t.Fatalf("got %d voices, want %d", len(body.Voices), len(orpheusVoices))
	}
	ids := make(map[string]bool, len(body.Voices))
	for _, v := range body.Voices {
		if v.ID == "" || v.Name == "" {
			t.Fatalf("voice entry missing id or name: %+v", v)
		}
		ids[v.ID] = true
	}
	for _, want := range []string{"autumn", "diana", "hannah", "austin", "daniel", "troy"} {
		if !ids[want] {
			t.Errorf("roster missing voice %q", want)
		}
	}
	if ids["alloy"] {
		t.Error("alloy must never be offered; groq/orpheus rejects it (#996)")
	}
}

func TestVoicesHandlerRejectsNonGet(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	VoicesHandler()(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/voices", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /v1/audio/voices: got %d want 405", rec.Code)
	}
}
