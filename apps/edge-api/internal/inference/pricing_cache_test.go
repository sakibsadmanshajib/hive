package inference

import (
	"bytes"
	"log"
	"os"
	"testing"
)

// hiveClaudeCacheRoute is an illustrative Anthropic-Sonnet-class fixture (no
// such alias exists in the live catalog today -- see the PR body for the
// current catalog's real aliases), priced in round numbers purely so the
// worked arithmetic below is checkable by hand: input 3,000,000
// credits/million, output 15,000,000, cache read 300,000 (0.1x input),
// cache write 3,750,000 (1.25x input).
var hiveClaudeCacheRoute = SelectRouteResult{
	AliasID:   "hive-claude-cache-demo",
	Pricing:   cachePricing(3_000_000, 15_000_000, 300_000, 3_750_000),
	PriceUnit: PriceUnitTokens,
}

// cachePricing builds a fixed-price row with explicit cache rates, the cache
// analogue of FixedPricing.
func cachePricing(inputCredits, outputCredits, cacheReadCredits, cacheWriteCredits int64) SelectRoutePricing {
	p := FixedPricing(inputCredits, outputCredits)
	p.CacheReadPriceCredits = &cacheReadCredits
	p.CacheWritePriceCredits = &cacheWriteCredits
	return p
}

// TestCreditsForTokensCacheAwarePricing is the audit's worked example:
// 200,000 cache-read + 5,000 fresh + 2,000 output must price at roughly one
// sixth of what the pre-fix flat-rate formula produced, never at the flat
// rate.
func TestCreditsForTokensCacheAwarePricing(t *testing.T) {
	fresh, cacheRead, cacheWrite, output := int64(5_000), int64(200_000), int64(0), int64(2_000)

	got := CreditsForTokens(hiveClaudeCacheRoute, fresh, cacheRead, cacheWrite, output)

	// fresh: 5000 * 3,000,000 = 15,000,000,000
	// cacheRead: 200000 * 300,000 = 60,000,000,000
	// output: 2000 * 15,000,000 = 30,000,000,000
	// sum = 105,000,000,000 / 1e6 = 105,000
	want := int64(105_000)
	if got != want {
		t.Fatalf("credits = %d, want %d", got, want)
	}

	// The pre-fix formula priced every prompt token (fresh + cache read) at
	// the flat input rate: (5000+200000)*3,000,000 + 2000*15,000,000 =
	// 615,000,000,000 + 30,000,000,000 = 645,000,000,000 / 1e6 = 645,000.
	oldFlat := int64(645_000)
	ratio := float64(oldFlat) / float64(got)
	if ratio < 6.0 || ratio > 6.3 {
		t.Errorf("old flat-rate charge / new cache-aware charge = %.3f, want ~6.14 (the audit's overcharge factor)", ratio)
	}
	if got == oldFlat {
		t.Error("credits still equal the flat-rate figure: cache pricing was not applied")
	}
}

// TestCreditsForTokensCacheWriteUndercharge is the write-side half of the
// audit: a first turn that populates cache must price the write premium
// (1.25x), never the flat input rate.
func TestCreditsForTokensCacheWriteUndercharge(t *testing.T) {
	fresh, cacheWrite, output := int64(0), int64(200_000), int64(2_000)

	got := CreditsForTokens(hiveClaudeCacheRoute, fresh, 0, cacheWrite, output)

	// cacheWrite: 200000 * 3,750,000 = 750,000,000,000
	// output: 2000 * 15,000,000 = 30,000,000,000
	// sum = 780,000,000,000 / 1e6 = 780,000
	want := int64(780_000)
	if got != want {
		t.Fatalf("credits = %d, want %d", got, want)
	}

	// The pre-fix flat formula: 200000*3,000,000 + 2000*15,000,000 =
	// 600,000,000,000 + 30,000,000,000 = 630,000,000,000 / 1e6 = 630,000,
	// about 19% under the correct 780,000.
	oldFlat := int64(630_000)
	if got == oldFlat {
		t.Error("credits still equal the flat-rate (undercharged) figure: cache-write pricing was not applied")
	}
	if got <= oldFlat {
		t.Errorf("credits = %d must exceed the old undercharged flat-rate figure %d", got, oldFlat)
	}
}

// TestCreditsForTokensExactBackwardCompatibility is invariant 4: with zero
// cache-read and zero cache-write, CreditsForTokens must return a value
// byte-identical to the pre-cache-aware formula (fresh treated as the old
// single "inputTokens" argument), across a range of token counts and rates.
// This is the regression guard that lets the cache-aware change merge
// safely.
func TestCreditsForTokensExactBackwardCompatibility(t *testing.T) {
	cases := []struct {
		name         string
		route        SelectRouteResult
		inputTokens  int64
		outputTokens int64
	}{
		{"hive-fast typical", hiveFastRoute, 12_000, 3_000},
		{"hive-fast tiny", hiveFastRoute, 72, 31},
		{"embedding, output-free", embeddingRoute, 500_000, 0},
		{"zero tokens", hiveFastRoute, 0, 0},
		{"large volume", hiveFastRoute, 2_000_000, 500_000},
		{"cache-priced route but zero cache quantities", hiveClaudeCacheRoute, 5_000, 2_000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := oldFlatCreditsForTokens(tc.route, tc.inputTokens, tc.outputTokens)
			got := CreditsForTokens(tc.route, tc.inputTokens, 0, 0, tc.outputTokens)
			if got != old {
				t.Errorf("CreditsForTokens(fresh=%d, 0, 0, %d) = %d, want byte-identical to the old formula's %d",
					tc.inputTokens, tc.outputTokens, got, old)
			}
		})
	}
}

// oldFlatCreditsForTokens is the pre-cache-aware formula, spelled out
// longhand (not calling any production helper) so this regression guard
// cannot silently start checking the new implementation against itself.
func oldFlatCreditsForTokens(route SelectRouteResult, inputTokens, outputTokens int64) int64 {
	if inputTokens < 0 {
		inputTokens = 0
	}
	if outputTokens < 0 {
		outputTokens = 0
	}
	numerator := inputTokens*route.Pricing.InputCredits() + outputTokens*route.Pricing.OutputCredits()
	credits := numerator / 1_000_000
	if (numerator%1_000_000)*2 >= 1_000_000 {
		credits++
	}
	if inputTokens+outputTokens > 0 && credits < 1 {
		credits = 1
	}
	return credits
}

// TestCreditsForTokensNeverChargesZeroForNonzeroCacheUsage is invariant 6,
// specifically for the case where ALL of the priced quantity is cache
// tokens (fresh=0, output=0): the floor must still apply.
func TestCreditsForTokensNeverChargesZeroForNonzeroCacheUsage(t *testing.T) {
	got := CreditsForTokens(hiveClaudeCacheRoute, 0, 1, 0, 0)
	if got < 1 {
		t.Errorf("credits = %d: a request that consumed one cache-read token must never settle at zero (D-034)", got)
	}
}

// TestResolveCacheRateFallsBackOnNilNotOnDeliberateZero is invariant 5: a
// NULL catalog price falls back to the documented multiplier and warns
// loudly; an EXPLICIT zero (a model the catalog author has determined has no
// cache premium, e.g. a "free cache write" provider) is honoured as the real
// rate, no fallback, no warning.
func TestResolveCacheRateFallsBackOnNilNotOnDeliberateZero(t *testing.T) {
	t.Run("nil price falls back to the default multiplier and warns", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)

		got := resolveCacheRate(nil, 10_000, DefaultCacheReadRateNum, DefaultCacheReadRateDenom, 500, "hive-test", "read")
		want := scaleRate(10_000, DefaultCacheReadRateNum, DefaultCacheReadRateDenom)
		if got != want {
			t.Errorf("resolveCacheRate = %d, want fallback %d", got, want)
		}
		if !bytes.Contains(buf.Bytes(), []byte("WARN")) || !bytes.Contains(buf.Bytes(), []byte("hive-test")) {
			t.Errorf("expected a WARN naming the alias when falling back, got: %q", buf.String())
		}
	})

	t.Run("explicit zero is honoured as a real free rate, no warning", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)

		zero := int64(0)
		got := resolveCacheRate(&zero, 10_000, DefaultCacheReadRateNum, DefaultCacheReadRateDenom, 500, "hive-test", "write")
		if got != 0 {
			t.Errorf("resolveCacheRate = %d, want 0 (the deliberate stored rate, not the fallback)", got)
		}
		if buf.Len() != 0 {
			t.Errorf("a deliberate zero price must not warn, got: %q", buf.String())
		}
	})

	t.Run("zero quantity never warns even when falling back", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)

		resolveCacheRate(nil, 10_000, DefaultCacheReadRateNum, DefaultCacheReadRateDenom, 0, "hive-test", "read")
		if buf.Len() != 0 {
			t.Errorf("a route that metered zero cache tokens must not warn just because its price is unset, got: %q", buf.String())
		}
	})
}

// TestAssertCacheBillingMagnitudeGuard is the runtime half of the
// magnitude-guard self-check (contract section 5): a charge within twice the
// flat-rate bound is silent, and a charge that exceeds it -- the signature of
// a semantics inversion -- logs a loud BUG line naming the alias.
func TestAssertCacheBillingMagnitudeGuard(t *testing.T) {
	t.Run("a correctly cache-aware charge never trips the guard", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)

		// The real charge from the worked example above; must not trip.
		assertCacheBillingMagnitude(hiveClaudeCacheRoute, 5_000, 200_000, 0, 2_000, 105_000)
		if bytes.Contains(buf.Bytes(), []byte("BUG:")) {
			t.Errorf("a correct, cache-aware charge tripped the magnitude guard: %q", buf.String())
		}
	})

	t.Run("a charge exceeding 2x the flat-rate bound trips the guard loudly", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)

		// totalPrompt=205000, output=2000: 2x ceiling = (205000*2*3,000,000 +
		// 2000*15,000,000)/1e6 = (1,230,000,000,000+30,000,000,000)/1e6 =
		// 1,260,000. Pass an implausible charge above that to simulate an
		// inverted-semantics bug.
		assertCacheBillingMagnitude(hiveClaudeCacheRoute, 5_000, 200_000, 0, 2_000, 1_260_001)
		if !bytes.Contains(buf.Bytes(), []byte("BUG:")) || !bytes.Contains(buf.Bytes(), []byte("hive-claude-cache-demo")) {
			t.Errorf("expected a loud BUG line naming the alias when the ceiling is breached, got: %q", buf.String())
		}
	})
}
