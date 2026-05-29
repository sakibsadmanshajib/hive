package payments

import "testing"

// TestUSDCentsToLocalPaisa_Exact locks the math/big FX invariant: the
// USD→BDT paisa conversion must be computed in exact rational arithmetic
// with no float64 cast (issue #114). The "float drift" case below is the
// regression guard — the previous implementation cast localRat.Float64()
// before truncating, which produced 123456788 instead of 123456789.
func TestUSDCentsToLocalPaisa_Exact(t *testing.T) {
	cases := []struct {
		name       string
		usdCents   int64
		rate       string
		wantPaisa  int64
	}{
		{
			name:      "whole rate",
			usdCents:  100, // $1.00
			rate:      "126.500000",
			wantPaisa: 12650,
		},
		{
			name:      "float drift case — old float64 path truncated to 123456788",
			usdCents:  1000000, // $10,000.00
			rate:      "123.456789",
			wantPaisa: 123456789,
		},
		{
			name:      "fractional rate truncates toward zero",
			usdCents:  3,
			rate:      "100.333333",
			wantPaisa: 300, // 3 * 100.333333 = 300.999999 -> floor 300
		},
		{
			name:      "zero amount",
			usdCents:  0,
			rate:      "126.500000",
			wantPaisa: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := usdCentsToLocalPaisa(tc.usdCents, tc.rate)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantPaisa {
				t.Fatalf("usdCentsToLocalPaisa(%d, %q) = %d, want %d", tc.usdCents, tc.rate, got, tc.wantPaisa)
			}
		})
	}
}

func TestUSDCentsToLocalPaisa_InvalidRate(t *testing.T) {
	if _, err := usdCentsToLocalPaisa(100, "not-a-number"); err == nil {
		t.Fatal("expected error for invalid effective rate, got nil")
	}
}
