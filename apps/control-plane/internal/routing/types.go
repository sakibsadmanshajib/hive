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

	// Pricing is the SELECTED ROUTE's per-route credit price (D-032), read
	// from public.provider_routes via Repository.LoadRoutePricing. It is
	// route-stable, not alias-stable: this is keyed by RouteID above, so a
	// fallback to a different route under the same alias carries that
	// route's own price, not a shared alias-wide number. This replaced the
	// original alias-stable design (metering step 2, spec decision 15) once
	// #617 showed one alias can route to providers whose real cost differs
	// by an order of magnitude (hive-fast: OpenRouter vs. Groq), so no
	// single alias-wide price could be correct for both.
	//
	// Additive field: an existing caller that only reads AliasID/RouteID/
	// LiteLLMModelName/Provider/FallbackRouteIDs keeps compiling and behaves
	// identically. A route with no price row fails the whole SelectRoute
	// call closed (see LoadRoutePricing) rather than returning a usable
	// result with a zero-value Pricing, so a caller that reaches this field
	// at all can trust it reflects a real, non-null price.
	//
	// The credit unit is per million tokens: charges compute as
	// (prompt_tokens * input_price + completion_tokens * output_price) / 1_000_000
	// with round-half-up via math/big. This is already implemented in
	// apps/edge-api/internal/metering/precedence.go:207-227 and matches
	// upstream canonical units (Groq, OpenRouter) and the console display.
	// Per-token integer pricing misprices 347 of 365 models by 1000x or more;
	// per-million fits all real rates and avoids repricing existing grants/invoices.
	// Decision D-031: vault decision-2026-07-31-credit-unit-per-million.md.
	// Issue #617 found the seeded prices themselves ~333x-7,375x too low
	// (non-uniform, ruling out a scale-factor bug); D-032 is the resulting
	// decision to reprice per-route. CacheReadPriceCredits/
	// CacheWritePriceCredits are always nil here: precedence.go's billing
	// formula never reads them, and provider_routes does not carry them.
	// catalog.Service's own /v1/models listing is unaffected by this change
	// and keeps serving model_aliases' cache price fields for display.
	Pricing catalog.CatalogPricing `json:"pricing"`
}
