package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// The alias id is published contract and is deliberately never redacted, so a
// leaky id is on the wire twice by design: models[].id and catalog[].id.
// Falling back to it for a redacted display field would put it there twice
// more, which is redaction increasing the leak. openrouter-auto is a real
// seeded row (20260822_30_openrouter_auto_variable_pricing.sql) whose own
// comment says flipping it to public is a one-line follow-up migration.
func TestFetchSnapshotDoesNotAmplifyALeakyAliasID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"models":[{"id":"openrouter-auto","object":"model","created":1716935002,"owned_by":"hive","name":"Openrouter Auto (Task Aware)","description":"Picks a model per request."}],
			"catalog":[{"id":"openrouter-auto","display_name":"Openrouter Auto (Task Aware)","summary":"Picks a model per request.","capability_badges":["chat"],"pricing":{"input_price_credits":null,"output_price_credits":null,"pricing_mode":"upstream_actual"},"lifecycle":"stable"}]
		}`))
	}))
	defer server.Close()

	snapshot, err := NewClient(server.URL).FetchSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}

	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	const wantOccurrences = 2 // models[].id and catalog[].id, nothing else
	if got := strings.Count(strings.ToLower(string(raw)), "openrouter"); got != wantOccurrences {
		t.Errorf("the published alias id must be the only carrier of the provider name: want %d occurrences, got %d in %s", wantOccurrences, got, raw)
	}
	if snapshot.Models[0].Name != "" {
		t.Errorf("name must not fall back to a leaky alias id, got %q", snapshot.Models[0].Name)
	}
	if snapshot.Catalog[0].DisplayName != "" {
		t.Errorf("display_name must not fall back to a leaky alias id, got %q", snapshot.Catalog[0].DisplayName)
	}
}

// redactSnapshot takes a Snapshot by value, but Models and Catalog are slice
// headers, so the indexed writes would otherwise scribble on whatever backing
// array the caller handed in. Harmless today because fetchSnapshot decodes a
// fresh value per call, and not harmless the day a cache lands in front of an
// unauthenticated endpoint that currently makes one HTTP call per request.
func TestRedactSnapshotLeavesTheCallersSlicesAlone(t *testing.T) {
	original := Snapshot{
		Models:  []Model{{ID: "hive-stt", Name: "Hive Voice STT (Groq)", Description: "Speech via Groq.", OwnedBy: "groq"}},
		Catalog: []CatalogModel{{ID: "hive-stt", DisplayName: "Hive Voice STT (Groq)", Summary: "Speech via Groq."}},
	}

	redacted := redactSnapshot(original)

	if original.Models[0].Name != "Hive Voice STT (Groq)" {
		t.Errorf("caller's model slice was mutated: %q", original.Models[0].Name)
	}
	if original.Catalog[0].DisplayName != "Hive Voice STT (Groq)" {
		t.Errorf("caller's catalog slice was mutated: %q", original.Catalog[0].DisplayName)
	}
	if redacted.Models[0].Name != "hive-stt" {
		t.Errorf("the returned copy should still be redacted, got %q", redacted.Models[0].Name)
	}
}

// fetchSnapshot deliberately replaces a nil Models or Catalog with an empty
// slice one line before calling redactSnapshot, so GET /v1/models serialises
// "models":[] rather than "models":null and a strict OpenAI client does not
// choke. The defensive copy inside redactSnapshot must not undo that:
// append([]Model(nil)) with nothing to append returns the nil first argument,
// so copying an empty-but-not-nil slice hands back a nil one.
func TestFetchSnapshotKeepsEmptyListsNonNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[],"catalog":[]}`))
	}))
	defer server.Close()

	snapshot, err := NewClient(server.URL).FetchSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FetchSnapshot: %v", err)
	}

	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if got := string(raw); got != `{"models":[],"catalog":[]}` {
		t.Errorf("empty lists must serialise as [] and not null, got %s", got)
	}
}

// The vocabulary is duplicated across the two Go modules rather than shared
// (see providerblind.go for why fifteen tokens do not justify a new module).
// This is the half of that trade that keeps the copies honest: the same table
// runs in apps/control-plane/internal/catalog, so a token added on one side
// and forgotten on the other fails here.
func TestContainsProviderIdentity(t *testing.T) {
	leaks := []string{
		"Serverless speech-to-text (Groq Whisper) for /v1/audio/transcriptions.",
		"Routed through OpenRouter when the pool is exhausted.",
		"served by route-groq-tts",
		"litellm proxy model",
		"NVIDIA NIM hosted",
		"together.ai hosted",
		"Vertex AI hosted",
		"Azure OpenAI hosted",
		"cerebras hosted",
		"GroqCloud",
		"OpenRouterAI",
		"FireworksAI",
		"CerebrasCloud",
		"Served by Together AI.",
		"Hosted on Google Vertex.",
		"GoogleVertexAI",
		"VertexAIStudio",
		"Runs on Azure ML.",
	}
	for _, s := range leaks {
		if !ContainsProviderIdentity(s) {
			t.Errorf("expected %q to be flagged as naming an upstream provider", s)
		}
	}

	clean := []string{
		"",
		"Serverless speech-to-text for /v1/audio/transcriptions.",
		"Deepseek V4 Pro",
		"Highest-capability long-context chat with tool use and reasoning, for harder work.",
		"Models that work together aiming at one answer.",
	}
	for _, s := range clean {
		if ContainsProviderIdentity(s) {
			t.Errorf("expected %q to pass, it names no upstream provider", s)
		}
	}
}
