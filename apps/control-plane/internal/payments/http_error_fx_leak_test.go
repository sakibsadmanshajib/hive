package payments

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// FX-17 second-pass review (P0 security) — classifyInitiateError MUST NOT
// echo the raw service-layer error string when that string carries FX
// rate values or other internal accounting detail. The legacy
// `errMsg = err.Error()` branch leaked
// `payments: invalid effective rate "115.500000"` onto a BD customer
// wire on a malformed-FX-snapshot failure path.
func TestClassifyInitiateError_NoRawFXRateLeak(t *testing.T) {
	t.Parallel()

	bannedFragments := []string{
		"effective rate",
		"115.5",
		"mid_rate",
		"fee_rate",
		"amount_usd",
		"fx_",
	}

	cases := []struct {
		name string
		err  error
	}{
		{
			name: "wrapped_invalid_effective_rate",
			err:  fmt.Errorf("payments: rail initiate: %w", fmt.Errorf("payments: invalid effective rate %q", "115.500000")),
		},
		{
			name: "direct_invalid_effective_rate",
			err:  fmt.Errorf("payments: invalid effective rate %q", "115.500000"),
		},
		{
			name: "resolve_price_per_credit",
			err:  fmt.Errorf("payments: resolve price per credit: %w", fmt.Errorf("payments: invalid effective rate %q", "115.500000")),
		},
		{
			name: "create_fx_snapshot",
			err:  fmt.Errorf("payments: create FX snapshot: %w", errors.New("fx: rate fetch failed mid_rate=110.00 fee_rate=0.05")),
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, msg := classifyInitiateError(tc.err)
			if status < 400 {
				t.Errorf("expected 4xx/5xx status for error path, got %d", status)
			}
			for _, banned := range bannedFragments {
				if strings.Contains(strings.ToLower(msg), strings.ToLower(banned)) {
					t.Errorf("classified error leaks banned fragment %q in wire message: %q", banned, msg)
				}
			}
		})
	}
}

// Customer-safe categories MUST still be surfaced verbatim — the message
// only contains the customer-provided value (credit count) or a static
// label, never internal accounting.
func TestClassifyInitiateError_SafeCategoriesPreserved(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		err      error
		wantCode int
		wantMsg  string
	}{
		{
			name:     "billing_profile_required",
			err:      ErrBillingProfileRequired,
			wantCode: 400,
			wantMsg:  "Complete billing profile required before first purchase",
		},
		{
			name:     "fx_unavailable_opaque",
			err:      ErrFXUnavailable,
			wantCode: 503,
			wantMsg:  "payment service temporarily unavailable",
		},
		{
			name:     "credits_must_be_positive",
			err:      errors.New("payments: credits must be positive, got -1"),
			wantCode: 400,
			wantMsg:  "credits must be positive",
		},
		{
			name:     "credits_multiple_of_1000",
			err:      errors.New("payments: credits must be a multiple of 1000, got 1500"),
			wantCode: 400,
			wantMsg:  "credits must be a multiple of 1000",
		},
		{
			name:     "unknown_defaults_opaque",
			err:      errors.New("payments: something exotic happened"),
			wantCode: 400,
			wantMsg:  "checkout failed",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotCode, gotMsg := classifyInitiateError(tc.err)
			if gotCode != tc.wantCode {
				t.Errorf("want code %d, got %d", tc.wantCode, gotCode)
			}
			if gotMsg != tc.wantMsg {
				t.Errorf("want msg %q, got %q", tc.wantMsg, gotMsg)
			}
		})
	}
}

// Defense-in-depth: ensure the "fx_unavailable" branch never echoes the
// underlying error string even if a future wrapper enriches it.
func TestClassifyInitiateError_FXUnavailableNeverEchoesUnderlying(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("payments: create FX snapshot: %w", ErrFXUnavailable)
	_, msg := classifyInitiateError(wrapped)
	for _, banned := range []string{"FX", "fx", "rate", "snapshot"} {
		if strings.Contains(msg, banned) {
			t.Errorf("FX-unavailable message leaks %q: %q", banned, msg)
		}
	}
}
