package inference

import (
	"strings"
	"testing"
)

// TestSettlementCredits is the RED-first spec for the settlement decision at
// the heart of the disconnect-settlement fix: what a stream should charge
// when it ends, and whether the reservation should be finalized (charged) or
// released in full. It replaces the old estimatedCredits fallback (a flat,
// hardcoded number) with either confirmed usage tokens or a content-based
// estimate -- never a made-up flat charge, and never zero when something was
// actually produced.
func TestSettlementCredits(t *testing.T) {
	tests := []struct {
		name          string
		hasUsage      bool
		totalTokens   int64
		promptBody    string
		content       string
		wantCredits   int64
		wantDelivered bool
	}{
		{
			name:          "confirmed usage wins over content estimate",
			hasUsage:      true,
			totalTokens:   250,
			content:       "irrelevant when usage is confirmed",
			wantCredits:   250,
			wantDelivered: true,
		},
		{
			name:          "no confirmed usage but content delivered: content-based estimate, floored at 1, never the flat 10000 estimate",
			hasUsage:      false,
			totalTokens:   0,
			content:       "partial reply the client never fully received",
			wantCredits:   estimateCompletionTokens("partial reply the client never fully received"),
			wantDelivered: true,
		},
		{
			name:          "no usage and no content: nothing delivered, release in full",
			hasUsage:      false,
			totalTokens:   0,
			content:       "",
			wantCredits:   0,
			wantDelivered: false,
		},
		{
			name:          "usage confirmed but reports zero total tokens: nothing delivered",
			hasUsage:      true,
			totalTokens:   0,
			content:       "",
			wantCredits:   0,
			wantDelivered: false,
		},
		{
			name:          "single character of content still floors at one credit, not zero",
			hasUsage:      false,
			totalTokens:   0,
			content:       "a",
			wantCredits:   1,
			wantDelivered: true,
		},
		{
			name:          "no confirmed usage: fallback bills the prompt too, not completion bytes alone (PR #602 finding 4 -- a long prompt aborted after one token must not settle for ~1 credit)",
			hasUsage:      false,
			totalTokens:   0,
			promptBody:    strings.Repeat("x", 4000),
			content:       "a",
			wantCredits:   estimateCompletionTokens(strings.Repeat("x", 4000)) + 1,
			wantDelivered: true,
		},
		{
			name:          "no confirmed usage and nothing delivered: prompt cost is NOT billed even with a large promptBody -- nothing delivered still means release in full",
			hasUsage:      false,
			totalTokens:   0,
			promptBody:    strings.Repeat("x", 4000),
			content:       "",
			wantCredits:   0,
			wantDelivered: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			credits, delivered := settlementCredits(tt.hasUsage, tt.totalTokens, tt.promptBody, tt.content)
			if credits != tt.wantCredits {
				t.Errorf("credits = %d, want %d", credits, tt.wantCredits)
			}
			if delivered != tt.wantDelivered {
				t.Errorf("delivered = %v, want %v", delivered, tt.wantDelivered)
			}
			if credits == 10000 {
				t.Errorf("credits must never fall back to the flat 10000 estimate")
			}
		})
	}
}
