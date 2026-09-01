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
	InputPriceCredits      *int64
	OutputPriceCredits     *int64
	CacheReadPriceCredits  *int64
	CacheWritePriceCredits *int64
	// PricingMode is model_aliases.pricing_mode: PricingModeFixed when the
	// price columns above are authoritative, PricingModeUpstreamActual when
	// they are NULL because the price is variable per request.
	PricingMode string
	// ReservationEstimateCredits is the up-front hold for an upstream_actual
	// alias, which has no catalog price to derive one from. NULL (nil) for a
	// fixed alias.
	ReservationEstimateCredits *int64
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
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

// PublicModel is one entry of the OpenAI-shaped model list served by
// edge-api's GET /v1/models.
//
// Name and Description are additive fields outside the OpenAI contract, and
// they are here for one reason: the chat model picker had no source of
// human-readable copy and therefore listed raw alias slugs with no explanation
// of what any of them is for. The same two strings have always existed in
// public.model_aliases (display_name, summary) and have always been rendered by
// the developer console's catalog table; they simply never travelled on the
// listing the picker reads.
//
// Both are `omitempty`, so an alias with neither serialises exactly the payload
// this endpoint served before, and a strict OpenAI client sees no new keys at
// all. Open WebUI merges unknown fields from an upstream model listing straight
// through (backend/open_webui/routers/openai.py builds `{**model, ...}`), which
// is what carries them to the picker without a second endpoint or a proxy.
type PublicModel struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created"`
	OwnedBy     string `json:"owned_by"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	// HiveCapabilities carries the Hive-owned capability facts a client needs
	// BEFORE it builds a request, which is the whole point of publishing it on
	// the model list rather than discovering it from a refusal.
	//
	// Deliberately NOT omitempty, and deliberately a value rather than a
	// pointer. Every entry carries the block, so "the field is absent" and "the
	// capability is false" can never be the same observation for a reader. An
	// absent block would be read as "old gateway, assume anything", which is
	// the failure this endpoint exists to prevent.
	HiveCapabilities ModelCapabilities `json:"hive_capabilities"`
}

// ModelCapabilities is the hive_capabilities block on one model list entry.
//
// Tools answers one question and only one: may a caller attach a tools block to
// a request on this alias without changing which routes the request can land
// on. See ToolCapableAliases in toolcapability.go for the rule and for why the
// question has to be answered here rather than at dispatch.
type ModelCapabilities struct {
	Tools bool `json:"tools"`
}

// Pricing modes, mirroring public.model_aliases.pricing_mode's CHECK
// constraint (supabase/migrations/20260822_30_openrouter_auto_variable_pricing.sql).
const (
	// PricingModeFixed: the price columns are authoritative. Every alias
	// except a variable-price router is this, and it is the column default,
	// so an older row that predates the column reads as fixed.
	PricingModeFixed = "fixed"
	// PricingModeUpstreamActual: the price is variable per request, the price
	// columns are NULL, and the charge comes from the cost the upstream
	// reports for that specific generation.
	PricingModeUpstreamActual = "upstream_actual"
)

// CatalogPricing carries an alias's price. The input and output figures are
// POINTERS rather than plain int64 on purpose: for a PricingModeUpstreamActual
// alias there is genuinely no price, and nil is the only honest way to say so.
// An int64 would have to say 0, which is not "no price" but "free", and a price
// that silently reads 0 is what billed nothing for three days in July. Every
// reader is therefore forced to decide what a missing price means instead of
// inheriting a zero by accident.
type CatalogPricing struct {
	InputPriceCredits      *int64 `json:"input_price_credits"`
	OutputPriceCredits     *int64 `json:"output_price_credits"`
	CacheReadPriceCredits  *int64 `json:"cache_read_price_credits,omitempty"`
	CacheWritePriceCredits *int64 `json:"cache_write_price_credits,omitempty"`
	// PricingMode says which of the two worlds this row is in. It is carried
	// beside the prices rather than inferred from their nil-ness so a reader
	// can tell "variable by design" from "a lookup that came back empty".
	PricingMode string `json:"pricing_mode"`
	// ReservationEstimateCredits sizes the up-front hold for a variable-price
	// alias. nil for a fixed one.
	ReservationEstimateCredits *int64 `json:"reservation_estimate_credits,omitempty"`
}

// FixedPricing builds an ordinary fixed-price row, in credits per million
// metered units, so callers state plain numbers rather than taking addresses
// of locals.
func FixedPricing(inputCredits, outputCredits int64) CatalogPricing {
	return CatalogPricing{
		InputPriceCredits:  &inputCredits,
		OutputPriceCredits: &outputCredits,
		PricingMode:        PricingModeFixed,
	}
}

// UpstreamActualPricing builds a variable-price row: no prices, a hold size,
// and the mode that tells settlement to charge the upstream's reported cost.
func UpstreamActualPricing(reservationEstimateCredits int64) CatalogPricing {
	return CatalogPricing{
		PricingMode:                PricingModeUpstreamActual,
		ReservationEstimateCredits: &reservationEstimateCredits,
	}
}

// HasFixedPrice reports whether this row carries a usable fixed price: the
// mode says fixed and at least one side is a positive number. One side at zero
// is legitimate (embeddings meter input only), both sides zero or absent is the
// no-cost-basis case routing refuses on (issue #617).
func (p CatalogPricing) HasFixedPrice() bool {
	if p.PricingMode == PricingModeUpstreamActual {
		return false
	}
	return derefPrice(p.InputPriceCredits) > 0 || derefPrice(p.OutputPriceCredits) > 0
}

// IsUpstreamActual reports whether the charge for this alias must be derived
// from the upstream's own reported cost rather than from these columns.
func (p CatalogPricing) IsUpstreamActual() bool {
	return p.PricingMode == PricingModeUpstreamActual
}

// derefPrice is deliberately unexported and deliberately NOT a general-purpose
// "give me a number" helper. Coercing a nil price to 0 is only ever correct
// when the question being asked is "is there a positive price here", which is
// the two predicates above. Anything that CHARGES must branch on the mode.
func derefPrice(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
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
