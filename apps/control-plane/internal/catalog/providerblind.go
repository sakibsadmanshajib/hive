package catalog

import (
	"log"
	"regexp"
	"strings"
	"sync"
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
//
// WHY THE TOKEN LIST HAS NO TRAILING \b
// A trailing word boundary fails in exactly the place a human writing a
// product name succeeds. In "GroqCloud", Groq's own product name, the q is
// followed by a C: word character to word character, so \b never matches and
// the whole string walks through clean. The same shape loses "OpenRouterAI",
// "FireworksAI" and "CerebrasCloud". Matching on the leading boundary only
// costs nothing here, because none of these eleven tokens is the prefix of a
// common English word. The vertex patterns drop it for the same reason, which
// is what catches GoogleVertexAI and VertexAIStudio: neither is English.
// "together" and "azure" ARE common English words, so those two keep their
// trailing boundary, or "we work together aiming at one answer" reads as
// together.ai and loses a whole description.
var providerIdentityRegex = regexp.MustCompile(`(?i)(?:` + strings.Join([]string{
	`\b(?:groq|openrouter|litellm|cerebras|fireworks|deepinfra|sambanova|novita|hyperbolic|perplexity|bedrock)`,
	`\bnvidia[ _-]?nim\b`,
	`\btogether[ ._-]?ai\b`,
	`\bvertex[ _-]?ai`,
	`\bgoogle[ _-]?vertex`,
	`\bazure[ _-]?(?:openai|ai|ml)\b`,
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
//	display_name      -> the alias id, which the payload already publishes,
//	                     unless the id is itself leaky (see displayFallback)
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
	if ContainsProviderIdentity(alias.AliasID) && firstSighting(alias.AliasID, "alias_id") {
		log.Printf("catalog_provider_identity_in_alias_id alias=%q; the id is published as-is and needs a migration to rename", alias.AliasID)
	}

	if ContainsProviderIdentity(alias.DisplayName) {
		logRedaction(alias.AliasID, "display_name", alias.DisplayName)
		alias.DisplayName = displayFallback(alias.AliasID)
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

// displayFallback is the alias id, unless the id is itself what the guard
// would flag.
//
// Falling back to a leaky id does not reduce the leak, it multiplies it.
// public.model_aliases carries alias_id 'openrouter-auto'
// (20260822_30_openrouter_auto_variable_pricing.sql, whose own comment says
// flipping its visibility to 'public' is a one-line follow-up migration), and
// the id is published as models[].id and catalog[].id by design. Copying it
// into a display field would put the provider name on the wire twice more than
// the published contract already requires.
//
// The empty string is the lesser defect: an empty display name renders as a
// gap in the console catalogue table, which is visible and fixable, while the
// alternative is a customer reading who serves the request. It is also the
// narrow case, since a clean alias id still gets the readable fallback.
func displayFallback(aliasID string) string {
	if ContainsProviderIdentity(aliasID) {
		return ""
	}
	return aliasID
}

// seen records which alias-and-field pairs have already been logged this
// process, so a leaky row on the unauthenticated GET /catalog/models produces
// one line rather than one line per request. Steady state is zero lines, so
// this only matters when something is already wrong, which is exactly when the
// log needs to stay readable.
//
// ponytail: unbounded in principle, bounded in practice by the number of
// catalogue rows, which is the same set the snapshot already holds in memory.
// Swap for a bounded LRU if aliases ever become user-generated.
var seen sync.Map

// The raw value is part of the key, not just the alias and field, so a row
// with two leaky capability badges still reports both and an edited row
// reports again.
func firstSighting(parts ...string) bool {
	_, duplicate := seen.LoadOrStore(strings.Join(parts, "\x00"), struct{}{})
	return !duplicate
}

// logRedaction records what was scrubbed, with the raw value, so the row can
// be repaired by migration. The raw value is safe here: this is an internal
// service log, and every other provider-blindness boundary in the repo logs
// the unsanitised text for the same reason (see
// apps/edge-api/internal/errors/provider_blind.go).
func logRedaction(aliasID, field, raw string) {
	if !firstSighting(aliasID, field, raw) {
		return
	}
	log.Printf("catalog_provider_identity_redacted alias=%q field=%q raw=%q", aliasID, field, raw)
}
