package inference

import (
	"bytes"
	"log"
	"os"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/metering"
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
	t.Run("nil price falls back to an exact fractional UnitCharge and warns", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)

		got := resolveCacheRate(nil, 10_000, DefaultCacheReadRateNum, DefaultCacheReadRateDenom, 500, "hive-test", "groq", "read")
		want := metering.UnitCharge{Quantity: 500 * DefaultCacheReadRateNum, CreditsPerMillion: 10_000, RateDivisor: DefaultCacheReadRateDenom}
		if got != want {
			t.Errorf("resolveCacheRate = %+v, want fallback %+v (exact fraction, not pre-rounded)", got, want)
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
		got := resolveCacheRate(&zero, 10_000, DefaultCacheReadRateNum, DefaultCacheReadRateDenom, 500, "hive-test", "groq", "write")
		if got != (metering.UnitCharge{Quantity: 500, CreditsPerMillion: 0}) {
			t.Errorf("resolveCacheRate = %+v, want the deliberate stored zero rate as an undivided charge", got)
		}
		if buf.Len() != 0 {
			t.Errorf("a deliberate zero price must not warn, got: %q", buf.String())
		}
	})

	t.Run("zero quantity never warns even when falling back", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)

		got := resolveCacheRate(nil, 10_000, DefaultCacheReadRateNum, DefaultCacheReadRateDenom, 0, "hive-test", "groq", "read")
		if got.Quantity != 0 {
			t.Errorf("zero-quantity fallback must contribute no quantity, got %+v", got)
		}
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

		// totalPrompt=205000, output=2000. The ceiling scales off the HIGHEST
		// of input/cache-read/cache-write (here cache-write's 3,750,000 beats
		// the flat input rate of 3,000,000): 2x ceiling =
		// (205000*2*3,750,000 + 2000*15,000,000)/1e6 =
		// (1,537,500,000,000+30,000,000,000)/1e6 = 1,567,500. Pass an
		// implausible charge above that to simulate an inverted-semantics bug.
		assertCacheBillingMagnitude(hiveClaudeCacheRoute, 5_000, 200_000, 0, 2_000, 1_567_501)
		if !bytes.Contains(buf.Bytes(), []byte("BUG:")) || !bytes.Contains(buf.Bytes(), []byte("hive-claude-cache-demo")) {
			t.Errorf("expected a loud BUG line naming the alias when the ceiling is breached, got: %q", buf.String())
		}
	})

	t.Run("ceiling scales off the highest cache rate, not the flat input rate alone", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)

		// A charge that WOULD have tripped the old input-rate-only ceiling
		// (1,260,000) but sits below the correct max-rate ceiling
		// (1,567,500) must not trip: this is exactly the false-positive the
		// fix removes.
		assertCacheBillingMagnitude(hiveClaudeCacheRoute, 5_000, 200_000, 0, 2_000, 1_300_000)
		if bytes.Contains(buf.Bytes(), []byte("BUG:")) {
			t.Errorf("guard false-tripped using the flat input rate instead of the highest of input/cache-read/cache-write: %q", buf.String())
		}
	})
}

// TestCreditsForTokensLoudlyClampsNegativeInputs is the review-requested
// fix: the four negative clamps in CreditsForTokens must log a BUG line
// naming the alias and the field, matching NormalizeCacheUsage's own
// clamp-and-alarm pattern, rather than clamping silently.
func TestCreditsForTokensLoudlyClampsNegativeInputs(t *testing.T) {
	cases := []struct {
		name                                 string
		fresh, cacheRead, cacheWrite, output int64
		wantField                            string
	}{
		{"negative fresh", -1, 0, 0, 1, "fresh_input_tokens"},
		{"negative cache read", 1, -1, 0, 1, "cache_read_tokens"},
		{"negative cache write", 1, 0, -1, 1, "cache_write_tokens"},
		{"negative output", 1, 0, 0, -1, "output_tokens"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log.SetOutput(&buf)
			defer log.SetOutput(os.Stderr)

			CreditsForTokens(hiveClaudeCacheRoute, tc.fresh, tc.cacheRead, tc.cacheWrite, tc.output)

			if !bytes.Contains(buf.Bytes(), []byte("BUG:")) || !bytes.Contains(buf.Bytes(), []byte(tc.wantField)) {
				t.Errorf("expected a loud BUG line naming %q, got: %q", tc.wantField, buf.String())
			}
		})
	}
}

// TestCreditsForTokensCounterOnMagnitudeGuardTrip confirms the magnitude
// guard actually increments hive_cache_billing_magnitude_guard_trips_total,
// not just logs, so the signal reaches Prometheus rather than only stdout.
func TestCreditsForTokensCounterOnMagnitudeGuardTrip(t *testing.T) {
	before := testutil.ToFloat64(cacheBillingMagnitudeGuardTrips.WithLabelValues(hiveClaudeCacheRoute.AliasID, hiveClaudeCacheRoute.Provider))

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	// Deliberately implausible: fresh dominates with no cache reduction at
	// all, at a route whose input rate alone already exceeds a sane bound
	// for this quantity is not how we trip it -- instead pass a credits
	// figure far past the real ceiling directly via the same route used
	// above, mirroring the "trips loudly" subtest.
	assertCacheBillingMagnitude(hiveClaudeCacheRoute, 5_000, 200_000, 0, 2_000, 1_567_501)

	after := testutil.ToFloat64(cacheBillingMagnitudeGuardTrips.WithLabelValues(hiveClaudeCacheRoute.AliasID, hiveClaudeCacheRoute.Provider))
	if after != before+1 {
		t.Errorf("cacheBillingMagnitudeGuardTrips = %v after a trip, want %v", after, before+1)
	}
}

// TestResolveCacheRateCounterOnFallback confirms the fallback path
// increments hive_cache_billing_fallback_rate_used_total.
func TestResolveCacheRateCounterOnFallback(t *testing.T) {
	before := testutil.ToFloat64(cacheBillingFallbackRateUsed.WithLabelValues("hive-counter-test", "groq", "read"))

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	resolveCacheRate(nil, 10_000, DefaultCacheReadRateNum, DefaultCacheReadRateDenom, 500, "hive-counter-test", "groq", "read")

	after := testutil.ToFloat64(cacheBillingFallbackRateUsed.WithLabelValues("hive-counter-test", "groq", "read"))
	if after != before+1 {
		t.Errorf("cacheBillingFallbackRateUsed = %v after a fallback, want %v", after, before+1)
	}
}

// TestCreditsForTokensFractionalFallbackSurvivesChargeArithmetic is the PR
// #1157 review regression: a fractional FALLBACK cache rate (the default
// multipliers of an alias with a NULL catalog cache price) must survive the
// charge arithmetic exactly. Pre-scaling "1/10 x input" into a whole
// credits-per-million integer collapses to zero for any alias priced below 5
// credits per million, dropping a request full of cache-read tokens to the
// 1-credit floor no matter how many tokens it consumed. The exact fraction
// must reach ChargeCredits instead, which folds fractional components onto a
// common denominator and keeps its single final round.
func TestCreditsForTokensFractionalFallbackSurvivesChargeArithmetic(t *testing.T) {
	route := SelectRouteResult{
		AliasID:   "hive-cheap-fallback-demo",
		Pricing:   FixedPricing(1, 2), // input 1 credit/million: below the 5/M collapse threshold
		PriceUnit: PriceUnitTokens,
	}

	t.Run("100M cache-read tokens on an unpriced alias charge 10 credits, not the floor", func(t *testing.T) {
		got := CreditsForTokens(route, 0, 100_000_000, 0, 0)
		if want := int64(10); got != want { // 100e6 * (1/10) / 1e6
			t.Fatalf("credits = %d, want %d", got, want)
		}
	})

	t.Run("100M cache-write tokens charge their exact 5/4x figure", func(t *testing.T) {
		got := CreditsForTokens(route, 0, 0, 100_000_000, 0)
		if want := int64(125); got != want { // 100e6 * 5/4 / 1e6
			t.Fatalf("credits = %d, want %d", got, want)
		}
	})

	t.Run("fractional parts sum before the single round", func(t *testing.T) {
		// fresh: 500_000 tokens at 1 credit/M = 0.5 credits; cache-read:
		// 15_000_000 tokens at 1/10 credit/M = 1.5 credits. Sum FIRST gives
		// exactly 2.0 -> 2 credits. Rounding each component independently
		// would give 1 + 2 = 3.
		got := CreditsForTokens(route, 500_000, 15_000_000, 0, 0)
		if want := int64(2); got != want {
			t.Fatalf("credits = %d, want %d (components must sum before rounding)", got, want)
		}
	})
}
