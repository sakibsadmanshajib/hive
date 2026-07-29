package inference

import "testing"

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			credits, delivered := settlementCredits(tt.hasUsage, tt.totalTokens, tt.content)
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
