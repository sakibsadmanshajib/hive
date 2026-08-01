package routing

import (
	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
)

type SelectionInput struct {
	AliasID string
	// TenantID scopes the selection to one tenant's model entitlement. It is
	// filled from the authenticated request context by the caller: edge-api
	// derives it from auth.TenantID(ctx) for JWT sessions, or from the
	// API-key's resolved tenant (authz.AuthSnapshot.TenantID, D-030) via
	// Orchestrator.selectRoute for API-key requests. Never from client input.
	//
	// uuid.Nil means no tenant could be bound: the batch executor runs per
	// account and never resolves one, and an API key whose account has no
	// tenant_billing_accounts row fails closed before ever reaching here
	// (edge-api refuses the request itself -- see
	// inference.ErrAccountNotProvisioned) rather than falling through to
	// this uuid.Nil skip-entitlement path.
	TenantID            uuid.UUID
	NeedResponses       bool
	NeedChatCompletions bool
	NeedEmbeddings      bool
	NeedStreaming       bool
	NeedReasoning       bool
	NeedCacheRead       bool
	NeedCacheWrite      bool
	NeedImageGeneration bool
	NeedImageEdit       bool
	NeedTTS             bool
	NeedSTT             bool
	NeedBatch           bool
	// RequireToolCapable, when true, restricts route selection to routes where
	// provider_capabilities.tools_supported = true. Returns ErrNoCapableRoute
	// when no such route exists for the alias.
	RequireToolCapable bool
	AllowedAliases     []string
	AllowedProviders   []string
}

type RouteCandidate struct {
	RouteID                 string
	AliasID                 string
	Provider                string
	ProviderModel           string
	LiteLLMModelName        string
	PriceClass              string
	HealthState             string
	Priority                int
	SupportsResponses       bool
	SupportsChatCompletions bool
	SupportsCompletions     bool
	SupportsEmbeddings      bool
	SupportsStreaming       bool
	SupportsReasoning       bool
	SupportsCacheRead       bool
	SupportsCacheWrite      bool
	SupportsImageGeneration bool
	SupportsImageEdit       bool
	SupportsTTS             bool
	SupportsSTT             bool
	SupportsBatch           bool
	SupportsTools           bool
}

type SelectionResult struct {
	AliasID          string   `json:"alias_id"`
	RouteID          string   `json:"route_id"`
	LiteLLMModelName string   `json:"litellm_model_name"`
	Provider         string   `json:"provider"`
	FallbackRouteIDs []string `json:"fallback_route_ids"`

	// Pricing is the alias's per-model credit price, read from
	// public.model_aliases via Repository.LoadAliasPricing. It is
	// alias-stable, not route-stable: every candidate SelectRoute could
	// have chosen for this alias (primary or any fallback) carries the same
	// price, so a fallback to a different route never changes it (metering
	// step 2 design, spec decision 15).
	//
	// Additive field: an existing caller that only reads AliasID/RouteID/
	// LiteLLMModelName/Provider/FallbackRouteIDs keeps compiling and behaves
	// identically. A caller that reads Pricing before an alias has a
	// model_aliases row sees the catalog.CatalogPricing zero value, which
	// must be read as "no cost basis available", never as "free"; this
	// step's own metering gate (edge-api internal/metering, added
	// separately) is what turns that distinction into a shadow-mode
	// verdict. Nothing in this package enforces or debits against it.
	//
	// The credit unit is per million tokens: charges compute as
	// (prompt_tokens * input_price + completion_tokens * output_price) / 1_000_000
	// with round-half-up via math/big. This is already implemented in
	// apps/edge-api/internal/metering/precedence.go:207-227 and matches
	// upstream canonical units (Groq, OpenRouter) and the console display.
	// Per-token integer pricing misprices 347 of 365 models by 1000x or more;
	// per-million fits all real rates and avoids repricing existing grants/invoices.
	// Decision D-031: vault decision-2026-07-31-credit-unit-per-million.md.
	// Issue #617 tracks catalog price correction (seeded prices are ~6 orders of magnitude too low).
	// These are the same raw input/output/cache price fields already served today by
	// catalog.Service for /v1/models, passed through unchanged.
	Pricing catalog.CatalogPricing `json:"pricing"`

	// PriceUnit is model_aliases.price_unit: the unit Pricing is quoted in,
	// per million. 'tokens' for text, 'characters' for speech synthesis,
	// 'seconds' for transcription. Text-to-speech is billed per character
	// upstream and transcription per unit of audio duration, so forcing either
	// into a per-token shape would mean inventing a rate; edge-api refuses a
	// request whose alias unit does not match what the endpoint meters rather
	// than converting between units (issue #627).
	//
	// For any non-token unit the price lives in Pricing.OutputPriceCredits and
	// InputPriceCredits is constrained to zero by a database CHECK
	// (supabase/migrations/20260801_13_alias_price_unit.sql), so a
	// single-quantity modality has exactly one price.
	PriceUnit string `json:"price_unit"`
}
