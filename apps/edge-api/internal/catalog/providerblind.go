package catalog

import (
	"log"
	"regexp"
	"strings"
)

// Edge-side provider-blindness guard for catalogue METADATA (issue #1284).
//
// GET /v1/models served, live, `"description":"Serverless speech-to-text (Groq
// Whisper) for /v1/audio/transcriptions."`. Control-plane now scrubs the same
// fields where the rows are read, and a migration repairs the two rows that
// leaked. This is the second half of the rule CLAUDE.md states for provider
// blindness: sanitize at BOTH the control-plane and the edge boundary. It is
// what covers a control-plane running one commit behind, a snapshot replayed
// from a stale Redis entry, and any future catalogue source wired into this
// client.
//
// The vocabulary is deliberately narrower than the error-path list in
// apps/edge-api/internal/errors/provider_blind.go: model VENDOR names are
// product copy this catalogue publishes on purpose ("Deepseek V4 Pro" is a
// shipped alias name), while this guard is about who SERVES the request. Keep
// it identical to apps/control-plane/internal/catalog/providerblind.go, which
// carries the full reasoning and the note on why fifteen tokens do not justify
// a shared Go module across the two.
var providerIdentityRegex = regexp.MustCompile(`(?i)(?:` + strings.Join([]string{
	`\b(?:groq|openrouter|litellm|cerebras|fireworks|deepinfra|sambanova|novita|hyperbolic|perplexity|bedrock)\b`,
	`\bnvidia[ _-]?nim\b`,
	`\btogether\.?ai\b`,
	`\bvertex[ _-]?ai\b`,
	`\bazure[ _-]?(?:openai|ai)\b`,
	`\broute-[a-z0-9][a-z0-9._/-]*`,
}, "|") + `)`)

// ContainsProviderIdentity reports whether s names an upstream serving
// provider or an internal route slug.
func ContainsProviderIdentity(s string) bool {
	return providerIdentityRegex.MatchString(s)
}

// redactSnapshot returns snapshot with every customer-visible copy field
// scrubbed of upstream provider identity. Degradation matches control-plane's:
// a leaky display name falls back to the alias id, a leaky summary is dropped,
// a leaky owned_by falls back to "hive", and a leaky capability badge is
// removed while the others survive. Alias ids are left alone: they are the
// customer's invocation handle, so blanking one serves an unusable listing.
func redactSnapshot(snapshot Snapshot) Snapshot {
	for i, model := range snapshot.Models {
		if ContainsProviderIdentity(model.Name) {
			logRedaction(model.ID, "name", model.Name)
			snapshot.Models[i].Name = model.ID
		}
		if ContainsProviderIdentity(model.Description) {
			logRedaction(model.ID, "description", model.Description)
			snapshot.Models[i].Description = ""
		}
		if ContainsProviderIdentity(model.OwnedBy) {
			logRedaction(model.ID, "owned_by", model.OwnedBy)
			snapshot.Models[i].OwnedBy = "hive"
		}
		if ContainsProviderIdentity(model.ID) {
			log.Printf("catalog_provider_identity_in_alias_id alias=%q; the id is published as-is and needs a migration to rename", model.ID)
		}
	}

	for i, entry := range snapshot.Catalog {
		if ContainsProviderIdentity(entry.DisplayName) {
			logRedaction(entry.ID, "display_name", entry.DisplayName)
			snapshot.Catalog[i].DisplayName = entry.ID
		}
		if ContainsProviderIdentity(entry.Summary) {
			logRedaction(entry.ID, "summary", entry.Summary)
			snapshot.Catalog[i].Summary = ""
		}
		if len(entry.CapabilityBadges) > 0 {
			kept := make([]string, 0, len(entry.CapabilityBadges))
			for _, badge := range entry.CapabilityBadges {
				if ContainsProviderIdentity(badge) {
					logRedaction(entry.ID, "capability_badges", badge)
					continue
				}
				kept = append(kept, badge)
			}
			snapshot.Catalog[i].CapabilityBadges = kept
		}
	}

	return snapshot
}

// logRedaction records what was scrubbed, with the raw value, so the row can
// be repaired by migration. Safe here: this is an internal service log, and
// every other provider-blindness boundary in this repo logs the unsanitised
// text for the same reason.
func logRedaction(aliasID, field, raw string) {
	log.Printf("catalog_provider_identity_redacted alias=%q field=%q raw=%q", aliasID, field, raw)
}
