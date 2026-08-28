package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
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

		// Vendor spellings a human actually writes, each of which the
		// pattern missed before. GroqCloud is Groq's own product name and a
		// trailing \b loses it: q followed by C is word character to word
		// character, so there is no boundary there. The same shape lost
		// OpenRouterAI, FireworksAI and CerebrasCloud. "Together AI" escaped
		// because it was the one multiword pattern refusing a separator.
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
		"Free-tier alias served from a load-balanced pool of our free provider keys.",
		"Hive Small",

		// Ordinary English that stays clean because the guard still needs a
		// separator or an adjacent token: "we work together aiming for" must
		// not read as together.ai.
		"Models that work together aiming at one answer.",
	}
	for _, s := range clean {
		if ContainsProviderIdentity(s) {
			t.Errorf("expected %q to pass, it names no upstream provider", s)
		}
	}
}

// ACCEPTED FALSE POSITIVES, pinned here on purpose.
//
// Four of the tokens this guard carries are also ordinary English, and their
// failure mode is silent: the description is dropped, the customer sees an
// empty field, and the only trace is one internal log line. Pinning them means
// the next person who finds a mysteriously blank description reads this list
// instead of filing a catalogue bug.
//
//   - perplexity, a standard language model evaluation metric, so "low
//     perplexity on long-context benchmarks" is plausible copy that gets
//     blanked.
//   - hyperbolic, bedrock and fireworks, plain English words, so "the bedrock
//     of our reasoning stack" and "produces fireworks on creative prompts"
//     are blanked.
//   - the route- slug pattern, which matches route-planning, so "great for
//     route-planning and logistics" is blanked.
//
// They stay because each is a real serving provider (Perplexity, Hyperbolic,
// Amazon Bedrock) or a real internal slug shape, and under-matching here puts
// a provider name in front of a customer while over-matching only costs a
// sentence. Rewrite the copy rather than widening the guard.
func TestContainsProviderIdentityAcceptedFalsePositives(t *testing.T) {
	for _, s := range []string{
		"Low perplexity on long-context benchmarks.",
		"The bedrock of our reasoning stack.",
		"Great for route-planning and logistics tasks.",
		"The model produces fireworks on creative prompts.",
	} {
		if !ContainsProviderIdentity(s) {
			t.Errorf("accepted false positive %q no longer matches; if that was deliberate, delete it from this list", s)
		}
	}
}

// The row where redaction could amplify the leak instead of removing it.
// 20260822_30_openrouter_auto_variable_pricing.sql seeds alias_id
// "openrouter-auto", and its own comment says flipping visibility to 'public'
// is a one-line follow-up migration, so this is a real row rather than a
// hypothetical one.
//
// alias_id is published contract and is deliberately left alone, so the
// payload always carries the provider name twice: models[].id and
// catalog[].id. What must not happen is a redacted field falling back to that
// same id and putting it on the wire two more times, which is redaction
// increasing the leak it exists to remove. The count is asserted over the
// whole serialised payload rather than field by field, so a field added to
// either wire shape later is covered without anyone remembering to extend it.
func TestSnapshotJSONDoesNotAmplifyALeakyAliasID(t *testing.T) {
	svc := NewService(&stubRepository{aliases: []ModelAlias{{
		AliasID:          "openrouter-auto",
		OwnedBy:          "hive",
		DisplayName:      "Openrouter Auto (Task Aware)",
		Summary:          "Picks a model per request.",
		Visibility:       "public",
		Lifecycle:        "stable",
		CapabilityBadges: []string{"chat"},
	}}})

	snapshot, err := svc.GetSnapshot(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
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

// GET /catalog/models is unauthenticated and holds no cache in front of the
// snapshot build, so a single leaky row would otherwise write one line per
// leaky field per request, at both boundaries, with the raw value in every
// line. An anonymous caller in a loop turns a copy defect into log volume,
// which is exactly when the log most needs to stay readable.
func TestLogRedactionSpeaksOncePerProcess(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	for i := 0; i < 5; i++ {
		logRedaction("hive-log-once", "summary", "Served by Groq.")
	}
	if got := strings.Count(buf.String(), "hive-log-once"); got != 1 {
		t.Errorf("want 1 line for a repeated redaction, got %d:\n%s", got, buf.String())
	}

	// A different raw value on the same field is a different fact and is
	// still reported, so an edited row does not hide behind the first one.
	logRedaction("hive-log-once", "summary", "Served by Cerebras.")
	if got := strings.Count(buf.String(), "hive-log-once"); got != 2 {
		t.Errorf("want a second line for a changed value, got %d:\n%s", got, buf.String())
	}
}

// A clean alias id still gets the readable fallback: blanking every display
// name would be its own defect in the console catalogue table, so the empty
// string above is the price of one leaky id, not the new default.
func TestRedactAliasStillFallsBackToACleanAliasID(t *testing.T) {
	redacted := redactAlias(ModelAlias{
		AliasID:     "hive-voice",
		DisplayName: "Hive Voice (Groq)",
	})
	if redacted.DisplayName != "hive-voice" {
		t.Errorf("display name should fall back to the clean alias id, got %q", redacted.DisplayName)
	}
}
