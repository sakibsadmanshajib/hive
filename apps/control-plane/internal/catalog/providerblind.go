package catalog

import (
	"log"
	"regexp"
	"strings"
)

// Provider-blindness guard for catalogue METADATA, as opposed to error bodies.
//
// Issue #1284: GET /v1/models served, live, `"description":"Serverless
// speech-to-text (Groq Whisper) for /v1/audio/transcriptions."`. The string
// was seeded into public.model_aliases.summary by
// supabase/migrations/20260717_02_voice_groq_stt_tts.sql and rendered verbatim
// by both wire shapes this package builds. Nothing between the row and the
// customer looked at it, because every provider-blindness guard this repo had
// sat on an error path.
//
// A migration repairs the two rows that leaked. This file is the reason the
// next one cannot: the guard runs on the way out, so a row seeded next month
// by a migration, by a psql session on the box, or by an admin surface that
// does not exist yet is scrubbed at the boundary rather than trusted.
//
// WHY THE VOCABULARY IS NARROWER THAN THE ERROR-PATH ONE
// apps/edge-api/internal/errors/provider_blind.go scrubs eighteen words,
// including anthropic, deepseek, google, mistral and openai. That is correct
// for an error message, whose text is disposable. It would be wrong here:
// model VENDOR names are product copy this catalogue publishes on purpose.
// deepseek-v4-pro and "Deepseek V4 Pro" are shipped, customer-facing names
// (supabase/migrations/20260822_02_catalog_alias_restructure.sql), so the
// eighteen-word list would rename two live models and blank their
// descriptions. The invariant is that a customer cannot tell WHO SERVES the
// request, not that no AI company may be named, so this list carries serving
// infrastructure: gateways, aggregators and hosting clouds, plus internal
// route slugs.
//
// ponytail: a plain literal list, duplicated in
// apps/edge-api/internal/catalog/providerblind.go rather than shared. Those
// are separate Go modules, so sharing means a new packages/ module wired into
// go.work, two go.mod files and five Dockerfiles for fifteen tokens. The repo
// already carries this duplication twice (the batch executor mirrors the
// edge-api error list). Promote all three to one shared module when a fourth
// copy is needed.
var providerIdentityRegex = regexp.MustCompile(`(?i)(?:` + strings.Join([]string{
	`\b(?:groq|openrouter|litellm|cerebras|fireworks|deepinfra|sambanova|novita|hyperbolic|perplexity|bedrock)\b`,
	`\bnvidia[ _-]?nim\b`,
	`\btogether\.?ai\b`,
	`\bvertex[ _-]?ai\b`,
	`\bazure[ _-]?(?:openai|ai)\b`,
	`\broute-[a-z0-9][a-z0-9._/-]*`,
}, "|") + `)`)

// ContainsProviderIdentity reports whether s names an upstream serving
// provider or an internal route slug. Exported so the tests that guard the
// customer-facing payloads can assert on the same predicate the redaction
// applies, rather than on a second copy of the vocabulary.
func ContainsProviderIdentity(s string) bool {
	return providerIdentityRegex.MatchString(s)
}

// redactAlias returns a copy of alias whose customer-visible copy fields name
// no upstream provider. It is called by every builder that produces a
// customer-facing wire shape from a row (buildCatalogSnapshot and
// buildPublicCatalogModels), which is every path out of this package to a
// customer.
//
// Each field degrades to something renderable rather than to a blank, because
// an empty display name in the console catalogue table is its own defect:
//
//	display_name      -> the alias id, which the payload already publishes
//	summary           -> dropped (omitempty on /v1/models, so the key vanishes)
//	owned_by          -> "hive", the column default every seeded row carries
//	capability_badges -> the offending badge only, the rest survive
//
// alias_id is NOT redacted and the row is NOT dropped. The id is the
// customer's invocation handle and is published contract: blanking it serves
// an unusable listing, and hiding the row removes a working model from every
// picker without telling anyone. A leaky id is a seeding mistake that needs a
// migration, so it is logged loudly and left visible instead.
func redactAlias(alias ModelAlias) ModelAlias {
	if ContainsProviderIdentity(alias.AliasID) {
		log.Printf("catalog_provider_identity_in_alias_id alias=%q; the id is published as-is and needs a migration to rename", alias.AliasID)
	}

	if ContainsProviderIdentity(alias.DisplayName) {
		logRedaction(alias.AliasID, "display_name", alias.DisplayName)
		alias.DisplayName = alias.AliasID
	}
	if ContainsProviderIdentity(alias.Summary) {
		logRedaction(alias.AliasID, "summary", alias.Summary)
		alias.Summary = ""
	}
	if ContainsProviderIdentity(alias.OwnedBy) {
		logRedaction(alias.AliasID, "owned_by", alias.OwnedBy)
		alias.OwnedBy = "hive"
	}

	// New slice rather than an in-place filter: alias is a copy but its badge
	// slice header still points at the caller's backing array.
	if len(alias.CapabilityBadges) > 0 {
		kept := make([]string, 0, len(alias.CapabilityBadges))
		for _, badge := range alias.CapabilityBadges {
			if ContainsProviderIdentity(badge) {
				logRedaction(alias.AliasID, "capability_badges", badge)
				continue
			}
			kept = append(kept, badge)
		}
		alias.CapabilityBadges = kept
	}

	return alias
}

// logRedaction records what was scrubbed, with the raw value, so the row can
// be repaired by migration. The raw value is safe here: this is an internal
// service log, and every other provider-blindness boundary in the repo logs
// the unsanitised text for the same reason (see
// apps/edge-api/internal/errors/provider_blind.go).
func logRedaction(aliasID, field, raw string) {
	log.Printf("catalog_provider_identity_redacted alias=%q field=%q raw=%q", aliasID, field, raw)
}
