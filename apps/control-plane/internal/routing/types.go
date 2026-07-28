package routing

import (
	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
)

type SelectionInput struct {
	AliasID string
	// TenantID scopes the selection to one tenant's model entitlement. It is
	// filled from the authenticated request context by the caller (edge-api
	// derives it from auth.TenantID(ctx)), never from client input.
	//
	// uuid.Nil means the principal is not tenant-scoped: API keys hang off
	// accounts, and the batch executor runs per account, so those selections are
	// governed by the key policy allowlist rather than tenant visibility.
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
	// The credit unit itself (per-token vs. per-million-tokens) is
	// unresolved pending the owner's ruling (spec section 12 item 1) and
	// blocks Step 4, not this addition: these are the same raw
	// input/output/cache price fields already served today by
	// catalog.Service for /v1/models, passed through unchanged.
	Pricing catalog.CatalogPricing `json:"pricing"`
}
