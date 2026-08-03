package inference

import (
	"strings"
	"testing"
)

// hiveFastRoute is a resolved route carrying hive-fast's row as of migration
// 20260801_01_alias_pricing_correction.sql: 10500 credits per million input
// tokens, 42000 per million output, priced in tokens. The row is a deliberately
// pinned historical one, superseded in the live catalog by
// 20260801_14_route_groq_fast_cheapest_model.sql; see the note at the top of
// settle_from_catalog_test.go for why it is not refreshed.
var hiveFastRoute = SelectRouteResult{
	AliasID:   "hive-fast",
	Pricing:   SelectRoutePricing{InputPriceCredits: 10_500, OutputPriceCredits: 42_000},
	PriceUnit: PriceUnitTokens,
}

// embeddingRoute is hive-embedding-default: input-priced only, at 1 credit per
// million tokens (supabase/migrations/20260423_01_embedding_alias.sql). One
// priced side and one zero side is legitimate, and the row is so cheap that the
// 1-credit floor is the whole charge below 1.5M tokens.
var embeddingRoute = SelectRouteResult{
	AliasID:   "hive-embedding-default",
	Pricing:   SelectRoutePricing{InputPriceCredits: 1},
	PriceUnit: PriceUnitTokens,
}

// TestSettlementCredits is the spec for the settlement decision: what a request
// should charge when it ends, whether that figure counts as measured, and
// whether the reservation should be finalized (charged) or released in full.
//
// The charge is the alias's catalog price applied to the metered token counts
// (#688), never the token counts themselves, and never the flat reservation
// estimate. Every priced case uses thousands of tokens: the 1-credit floor
// hides a wrong conversion at small counts.
func TestSettlementCredits(t *testing.T) {
	tests := []struct {
		name          string
		route         SelectRouteResult
		hasUsage      bool
		inputTokens   int64
		outputTokens  int64
		promptBody    string
		content       string
		wantCredits   int64
		wantConfirmed bool
		wantDelivered bool
	}{
		{
			name:         "confirmed usage is priced from the catalog, not counted as credits",
			route:        hiveFastRoute,
			hasUsage:     true,
			inputTokens:  12_000,
			outputTokens: 3_000,
			content:      "irrelevant when usage is confirmed",
			// 12000 * 10500 + 3000 * 42000 = 252_000_000, / 1e6 = 252.
			wantCredits:   252,
			wantConfirmed: true,
			wantDelivered: true,
		},
		{
			name:         "the reported 72-in/31-out request settles at its catalog price, never at 103",
			route:        hiveFastRoute,
			hasUsage:     true,
			inputTokens:  72,
			outputTokens: 31,
			// 72 * 10500 + 31 * 42000 = 2_058_000, / 1e6 = 2.058, round half up = 2.
			wantCredits:   2,
			wantConfirmed: true,
			wantDelivered: true,
		},
		{
			name:     "no usage block: the estimate is catalog-priced too, and flagged unconfirmed",
			route:    hiveFastRoute,
			hasUsage: false,
			// 1000 estimated prompt tokens (12,000 bytes at bytesPerToken, #673)
			// + 1000 completion. Neither 'x' nor 'y' is runCollapsible, so the
			// byte length is counted in full.
			promptBody: strings.Repeat("x", 12_000),
			content:    strings.Repeat("y", 12_000),
			// 1000 * 10500 + 1000 * 42000 = 52_500_000, / 1e6 = 52.5, round half up = 53.
			wantCredits:   53,
			wantConfirmed: false,
			wantDelivered: true,
		},
		{
			name:          "no usage and no content: nothing delivered, release in full",
			route:         hiveFastRoute,
			hasUsage:      false,
			wantCredits:   0,
			wantConfirmed: false,
			wantDelivered: false,
		},
		{
			name:          "usage block reporting no tokens at all with nothing delivered: release in full",
			route:         hiveFastRoute,
			hasUsage:      true,
			wantCredits:   0,
			wantConfirmed: false,
			wantDelivered: false,
		},
		{
			name:     "usage block reporting no tokens but content delivered: estimated, never free and never confirmed",
			route:    hiveFastRoute,
			hasUsage: true,
			content:  strings.Repeat("y", 12_000),
			// 0 prompt tokens (no body) + 1000 completion (12,000 bytes at
			// bytesPerToken, #673) = 42_000_000, / 1e6 = 42.
			wantCredits:   42,
			wantConfirmed: false,
			wantDelivered: true,
		},
		{
			name:          "a single character of content still floors at one credit, never zero",
			route:         hiveFastRoute,
			hasUsage:      false,
			content:       "a",
			wantCredits:   1,
			wantConfirmed: false,
			wantDelivered: true,
		},
		{
			name:         "an input-only alias prices the input side alone",
			route:        embeddingRoute,
			hasUsage:     true,
			inputTokens:  500_000,
			outputTokens: 0,
			// 500000 * 1 = 500_000, / 1e6 = 0.5, round half up = 1.
			wantCredits:   1,
			wantConfirmed: true,
			wantDelivered: true,
		},
		{
			name:         "a negative reported token count cannot subtract from the charge",
			route:        hiveFastRoute,
			hasUsage:     true,
			inputTokens:  12_000,
			outputTokens: -3_000,
			// The negative side is clamped away: 12000 * 10500 = 126_000_000, / 1e6 = 126.
			wantCredits:   126,
			wantConfirmed: true,
			wantDelivered: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			credits, confirmed, delivered := settlementCredits(
				tt.route, tt.hasUsage, tt.inputTokens, tt.outputTokens, tt.promptBody, tt.content)
			if credits != tt.wantCredits {
				t.Errorf("credits = %d, want %d", credits, tt.wantCredits)
			}
			if confirmed != tt.wantConfirmed {
				t.Errorf("confirmed = %v, want %v", confirmed, tt.wantConfirmed)
			}
			if delivered != tt.wantDelivered {
				t.Errorf("delivered = %v, want %v", delivered, tt.wantDelivered)
			}
			if credits == 10000 {
				t.Errorf("credits must never fall back to the flat 10000 estimate")
			}
			if tt.inputTokens > 0 && credits == tt.inputTokens+tt.outputTokens {
				t.Errorf("credits = %d: one credit per token, the catalog was not consulted (#688)", credits)
			}
		})
	}
}

// TestCanPriceTokens covers the fail-closed gate's own decision: only an alias
// whose catalog row is explicitly quoted in tokens, with at least one priced
// side, can be charged by these endpoints.
func TestCanPriceTokens(t *testing.T) {
	tests := []struct {
		name  string
		route SelectRouteResult
		want  bool
	}{
		{"token-priced on both sides", hiveFastRoute, true},
		{"input-priced only (embeddings)", embeddingRoute, true},
		{
			name: "output-priced only",
			route: SelectRouteResult{
				Pricing: SelectRoutePricing{OutputPriceCredits: 42_000}, PriceUnit: PriceUnitTokens},
			want: true,
		},
		{
			name: "priced per second (voice): this endpoint cannot meter it",
			route: SelectRouteResult{
				Pricing: SelectRoutePricing{OutputPriceCredits: 4_316_667}, PriceUnit: "seconds"},
			want: false,
		},
		{
			name: "priced per character (speech): this endpoint cannot meter it",
			route: SelectRouteResult{
				Pricing: SelectRoutePricing{OutputPriceCredits: 3_080_000}, PriceUnit: "characters"},
			want: false,
		},
		{
			name: "no unit at all: implicit is not explicit",
			route: SelectRouteResult{
				Pricing: SelectRoutePricing{InputPriceCredits: 10_500, OutputPriceCredits: 42_000}},
			want: false,
		},
		{
			name:  "token unit but no price on either side",
			route: SelectRouteResult{PriceUnit: PriceUnitTokens},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanPriceTokens(tt.route); got != tt.want {
				t.Errorf("CanPriceTokens() = %v, want %v", got, tt.want)
			}
		})
	}
}
