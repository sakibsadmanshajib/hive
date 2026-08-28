package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// Edge half of the issue #1284 guard. Control-plane scrubs the same fields at
// its own boundary; this one exists because edge-api is the surface the
// customer actually reads, and CLAUDE.md requires provider blindness to be
// enforced at both. It is what keeps a control-plane deployed one commit
// behind, or a snapshot served from a stale Redis entry, from putting a
// provider name back on GET /v1/models.
const leakySnapshotJSON = `{
	"models":[
		{"id":"hive-stt","object":"model","created":1716935002,"owned_by":"groq","name":"Hive Voice STT (Groq)","description":"Serverless speech-to-text (Groq Whisper) for /v1/audio/transcriptions."},
		{"id":"hive-tts","object":"model","created":1716935002,"owned_by":"hive","name":"Hive Voice TTS","description":"Serverless text-to-speech (Groq PlayAI) for /v1/audio/speech."}
	],
	"catalog":[
		{"id":"hive-stt","display_name":"Hive Voice STT (Groq)","summary":"Serverless speech-to-text (Groq Whisper) for /v1/audio/transcriptions.","capability_badges":["voice","groq-hosted","stt"],"pricing":{"input_price_credits":0,"output_price_credits":500,"pricing_mode":"fixed"},"lifecycle":"stable"}
	]
}`

func leakySnapshotServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(leakySnapshotJSON))
	}))
}

func TestFetchSnapshotRedactsUpstreamProviderIdentity(t *testing.T) {
	server := leakySnapshotServer(t)
	defer server.Close()

	snapshot, err := NewClient(server.URL).FetchSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}

	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if ContainsProviderIdentity(string(raw)) {
		t.Errorf("snapshot served to customers names an upstream provider: %s", raw)
	}
	if len(snapshot.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(snapshot.Models))
	}
	if snapshot.Models[0].ID != "hive-stt" {
		t.Errorf("alias id must survive redaction, got %q", snapshot.Models[0].ID)
	}
	if snapshot.Models[0].OwnedBy != "hive" {
		t.Errorf("owned_by should fall back to hive, got %q", snapshot.Models[0].OwnedBy)
	}
	if snapshot.Models[0].Name != "hive-stt" {
		t.Errorf("name should fall back to the alias id, got %q", snapshot.Models[0].Name)
	}
	if snapshot.Models[1].Description != "" {
		t.Errorf("description should be dropped, got %q", snapshot.Models[1].Description)
	}
	badges := snapshot.Catalog[0].CapabilityBadges
	if len(badges) != 2 || badges[0] != "voice" || badges[1] != "stt" {
		t.Errorf("only the provider badge should be dropped, got %v", badges)
	}
}

func TestFetchSnapshotForTenantRedactsUpstreamProviderIdentity(t *testing.T) {
	server := leakySnapshotServer(t)
	defer server.Close()

	snapshot, err := NewClient(server.URL).FetchSnapshotForTenant(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("FetchSnapshotForTenant: %v", err)
	}

	for _, model := range snapshot.Models {
		if ContainsProviderIdentity(model.Description) || ContainsProviderIdentity(model.Name) || ContainsProviderIdentity(model.OwnedBy) {
			t.Errorf("model %q still names an upstream provider: %+v", model.ID, model)
		}
	}
}

// Model vendor names are deliberate product copy and must survive. Scrubbing
// them would rename shipped models such as deepseek-v4-pro.
func TestFetchSnapshotKeepsModelVendorNaming(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"models":[{"id":"deepseek-v4-pro","object":"model","created":1716935002,"owned_by":"hive","name":"Deepseek V4 Pro","description":"Highest-capability long-context chat with tool use and reasoning, for harder work."}],
			"catalog":[]
		}`))
	}))
	defer server.Close()

	snapshot, err := NewClient(server.URL).FetchSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}
	if snapshot.Models[0].Name != "Deepseek V4 Pro" {
		t.Errorf("vendor naming was altered: %q", snapshot.Models[0].Name)
	}
	if snapshot.Models[0].Description == "" {
		t.Error("description of a clean row was dropped")
	}
}
