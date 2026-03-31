package routing

type SelectionInput struct {
	AliasID             string
	NeedResponses       bool
	NeedChatCompletions bool
	NeedEmbeddings      bool
	NeedStreaming       bool
	NeedReasoning       bool
	NeedCacheRead       bool
	NeedCacheWrite      bool
	AllowedAliases      []string
	AllowedProviders    []string
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
}

type SelectionResult struct {
	AliasID          string   `json:"alias_id"`
	RouteID          string   `json:"route_id"`
	LiteLLMModelName string   `json:"litellm_model_name"`
	Provider         string   `json:"provider"`
	FallbackRouteIDs []string `json:"fallback_route_ids"`
}
