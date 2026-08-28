package catalog

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// These tests are the regression guard for issue #1284: GET /v1/models served
// `"description":"Serverless speech-to-text (Groq Whisper) for
// /v1/audio/transcriptions."` in production, naming the upstream serving
// provider in a field every caller of the model list reads.
//
// The two rows below are the live seeded values from
// supabase/migrations/20260717_02_voice_groq_stt_tts.sql, verbatim, so the
// test fails on exactly the payload the OpenAI SDK observed against
// api-hive.scubed.co rather than on a paraphrase of it.
func leakyVoiceAliases() []ModelAlias {
	return []ModelAlias{
		{
			AliasID:     "hive-stt",
			OwnedBy:     "hive",
			DisplayName: "Hive Voice STT",
			Summary:     "Serverless speech-to-text (Groq Whisper) for /v1/audio/transcriptions.",
			Visibility:  "public",
			Lifecycle:   "stable",
		},
		{
			AliasID:     "hive-tts",
			OwnedBy:     "hive",
			DisplayName: "Hive Voice TTS",
			Summary:     "Serverless text-to-speech (Groq PlayAI) for /v1/audio/speech.",
			Visibility:  "public",
			Lifecycle:   "stable",
		},
	}
}

func TestSnapshotRedactsUpstreamProviderFromModelDescriptions(t *testing.T) {
	svc := NewService(&stubRepository{aliases: leakyVoiceAliases()})

	snapshot, err := svc.GetSnapshot(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}

	if len(snapshot.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(snapshot.Models))
	}
	for _, model := range snapshot.Models {
		if ContainsProviderIdentity(model.Description) {
			t.Errorf("model %q description names an upstream provider: %q", model.ID, model.Description)
		}
	}
	for _, entry := range snapshot.Catalog {
		if ContainsProviderIdentity(entry.Summary) {
			t.Errorf("catalog entry %q summary names an upstream provider: %q", entry.ID, entry.Summary)
		}
	}
}

func TestTenantSnapshotRedactsUpstreamProviderFromModelDescriptions(t *testing.T) {
	svc := NewService(&stubRepository{aliases: leakyVoiceAliases()})

	snapshot, err := svc.GetSnapshotForTenant(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("GetSnapshotForTenant: %v", err)
	}

	if len(snapshot.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(snapshot.Models))
	}
	for _, model := range snapshot.Models {
		if ContainsProviderIdentity(model.Description) {
			t.Errorf("model %q description names an upstream provider: %q", model.ID, model.Description)
		}
	}
}

// /api/v1/catalog/models is the second wire shape built from the same rows and
// is reachable without a bearer token. It gets the same guard, in the same
// change, because fixing only the endpoint an issue names is how this leak
// family keeps coming back.
func TestPublicCatalogModelsRedactsUpstreamProviderFromSummaries(t *testing.T) {
	models := buildPublicCatalogModels(leakyVoiceAliases())

	if len(models) != 2 {
		t.Fatalf("expected 2 catalog models, got %d", len(models))
	}
	for _, model := range models {
		if ContainsProviderIdentity(model.Summary) {
			t.Errorf("catalog model %q summary names an upstream provider: %q", model.ID, model.Summary)
		}
		if ContainsProviderIdentity(model.DisplayName) {
			t.Errorf("catalog model %q display name names an upstream provider: %q", model.ID, model.DisplayName)
		}
	}
}

// Every customer-visible copy field at once, asserted over the serialised
// payload rather than field by field, so a field added to either wire shape
// later is covered by this test without anyone remembering to extend it.
func TestSnapshotJSONCarriesNoUpstreamProviderIdentity(t *testing.T) {
	svc := NewService(&stubRepository{aliases: []ModelAlias{{
		AliasID:          "hive-voice",
		OwnedBy:          "groq",
		DisplayName:      "Hive Voice (OpenRouter)",
		Summary:          "Speech served by route-groq-tts through LiteLLM.",
		Visibility:       "public",
		Lifecycle:        "stable",
		CapabilityBadges: []string{"voice", "groq-hosted", "tts"},
	}}})

	snapshot, err := svc.GetSnapshot(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}

	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if ContainsProviderIdentity(string(raw)) {
		t.Errorf("serialised catalog snapshot names an upstream provider: %s", raw)
	}
}

func TestRedactAliasFallsBackWithoutBlankingTheRow(t *testing.T) {
	redacted := redactAlias(ModelAlias{
		AliasID:          "hive-voice",
		OwnedBy:          "groq",
		DisplayName:      "Hive Voice (Groq)",
		Summary:          "Text to speech via Groq.",
		CapabilityBadges: []string{"voice", "groq-hosted", "tts"},
	})

	if redacted.DisplayName != "hive-voice" {
		t.Errorf("display name should fall back to the alias id, got %q", redacted.DisplayName)
	}
	if redacted.Summary != "" {
		t.Errorf("summary should be dropped, got %q", redacted.Summary)
	}
	if redacted.OwnedBy != "hive" {
		t.Errorf("owned_by should fall back to hive, got %q", redacted.OwnedBy)
	}
	if len(redacted.CapabilityBadges) != 2 || redacted.CapabilityBadges[0] != "voice" || redacted.CapabilityBadges[1] != "tts" {
		t.Errorf("only the provider badge should be dropped, got %v", redacted.CapabilityBadges)
	}
}

// The vocabulary is deliberately narrower than the error-path one in
// apps/edge-api/internal/errors/provider_blind.go. Model VENDOR names are
// product copy this catalogue publishes on purpose: deepseek-v4-pro and
// "Deepseek V4 Pro" are shipped, customer-facing names
// (supabase/migrations/20260822_02_catalog_alias_restructure.sql). Scrubbing
// them would blank two live descriptions and rename two live models, which is
// why the guard matches serving-provider identity, not every AI company.
func TestRedactAliasLeavesModelVendorNamingIntact(t *testing.T) {
	alias := ModelAlias{
		AliasID:          "deepseek-v4-pro",
		OwnedBy:          "hive",
		DisplayName:      "Deepseek V4 Pro",
		Summary:          "Highest-capability long-context chat with tool use and reasoning, for harder work.",
		CapabilityBadges: []string{"stable", "chat", "responses", "tools", "reasoning"},
	}

	redacted := redactAlias(alias)

	if redacted.DisplayName != alias.DisplayName {
		t.Errorf("display name was altered: %q", redacted.DisplayName)
	}
	if redacted.Summary != alias.Summary {
		t.Errorf("summary was altered: %q", redacted.Summary)
	}
	if len(redacted.CapabilityBadges) != len(alias.CapabilityBadges) {
		t.Errorf("badges were altered: %v", redacted.CapabilityBadges)
	}
}

func TestContainsProviderIdentity(t *testing.T) {
	leaks := []string{
		"Serverless speech-to-text (Groq Whisper) for /v1/audio/transcriptions.",
		"Serverless text-to-speech (Groq PlayAI) for /v1/audio/speech.",
		"Routed through OpenRouter when the pool is exhausted.",
		"served by route-groq-tts",
		"litellm proxy model",
		"NVIDIA NIM hosted",
		"together.ai hosted",
		"Vertex AI hosted",
		"Azure OpenAI hosted",
		"cerebras hosted",
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
		"Free-tier alias served from a load-balanced pool of our free provider keys.",
		"Hive Small",
	}
	for _, s := range clean {
		if ContainsProviderIdentity(s) {
			t.Errorf("expected %q to pass, it names no upstream provider", s)
		}
	}
}
