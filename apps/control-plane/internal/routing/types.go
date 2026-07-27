package routing

import "github.com/google/uuid"

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
}
