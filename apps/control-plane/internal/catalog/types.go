package catalog

import (
	"time"

	"github.com/google/uuid"
)

// TenantModelVisibility records whether a specific tenant has been granted or
// blocked access to a model alias. Rows only exist when the default behaviour
// (public aliases visible, restricted aliases hidden) is overridden.
type TenantModelVisibility struct {
	TenantID  uuid.UUID
	AliasID   string
	Visible   bool
	UpdatedAt time.Time
}

type ModelAlias struct {
	AliasID                string
	OwnedBy                string
	DisplayName            string
	Summary                string
	Visibility             string
	Lifecycle              string
	CapabilityBadges       []string
	InputPriceCredits      int64
	OutputPriceCredits     int64
	CacheReadPriceCredits  *int64
	CacheWritePriceCredits *int64
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type RouteSnapshot struct {
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

type AliasPolicySnapshot struct {
	AliasID                 string
	PolicyMode              string
	AllowPriceClassWidening bool
	FallbackOrder           []string
}

type PublicModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type CatalogPricing struct {
	InputPriceCredits      int64  `json:"input_price_credits"`
	OutputPriceCredits     int64  `json:"output_price_credits"`
	CacheReadPriceCredits  *int64 `json:"cache_read_price_credits,omitempty"`
	CacheWritePriceCredits *int64 `json:"cache_write_price_credits,omitempty"`
}

type PublicCatalogModel struct {
	ID               string         `json:"id"`
	DisplayName      string         `json:"display_name"`
	Summary          string         `json:"summary"`
	CapabilityBadges []string       `json:"capability_badges"`
	Pricing          CatalogPricing `json:"pricing"`
	Lifecycle        string         `json:"lifecycle"`
}

type CatalogSnapshot struct {
	Models        []PublicModel         `json:"models"`
	Catalog       []PublicCatalogModel  `json:"catalog"`
	Routes        []RouteSnapshot       `json:"-"`
	AliasPolicies []AliasPolicySnapshot `json:"-"`
}
