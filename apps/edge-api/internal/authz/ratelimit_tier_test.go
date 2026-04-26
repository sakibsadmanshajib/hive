package authz

import (
	"context"
	"strings"
	"testing"
	"time"
)

// CheckWithTier should layer a tier scope after key+account, with effective
// per-dimension limit = min(keyLimit, tierLimit).
func TestCheckWithTierEnforcesMinKeyOrTierRPM(t *testing.T) {
	var calls []slidingWindowCall

	limiter := &Limiter{
		now: func() time.Time { return time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC) },
		runSlidingWindow: func(_ context.Context, keys []string, limit int, amount int64, _ time.Time) (bool, int, int, error) {
			calls = append(calls, slidingWindowCall{keys: append([]string(nil), keys...), limit: limit, amount: amount})
			return true, limit - 1, 30, nil
		},
		runLongWindow: func(_ context.Context, _, _ string, _ time.Duration, _ int, limit int64, score int64, _ time.Time) (bool, int64, int, error) {
			return true, limit - score, 300, nil
		},
	}

	snapshot := AuthSnapshot{
		KeyID:             "key-1",
		AccountID:         "acc-1",
		AccountRatePolicy: &RatePolicy{RateLimitRPM: 1000, RateLimitTPM: 1000000, FreeTokenWeightTenths: 1},
		KeyRatePolicy:     &RatePolicy{RateLimitRPM: 60, RateLimitTPM: 4000, FreeTokenWeightTenths: 1},
	}
	tierLimits := TierLimits{RPM: 10, TPM: 2000} // tier is tighter

	result, err := limiter.CheckWithTier(context.Background(), snapshot, "alias-x", TierGuest, tierLimits, 0, 100, 0)
	if err != nil {
		t.Fatalf("CheckWithTier: %v", err)
	}
	if !result.Allowed {
		t.Fatalf("expected allowed, got result %#v", result)
	}

	// Find the tier RPM call.
	var tierRPMLimit int
	for _, c := range calls {
		if strings.Contains(c.keys[0], "rl:{tier:guest:acc-1}:rpm:current") {
			tierRPMLimit = c.limit
		}
	}
	if tierRPMLimit != 10 {
		t.Fatalf("expected tier scope to use min(60,10)=10, got %d", tierRPMLimit)
	}
}

func TestCheckWithTierUsesKeyLimitWhenTighter(t *testing.T) {
	var seenLimits []int
	limiter := &Limiter{
		now: func() time.Time { return time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC) },
		runSlidingWindow: func(_ context.Context, keys []string, limit int, _ int64, _ time.Time) (bool, int, int, error) {
			if strings.Contains(keys[0], "rl:{tier:") {
				seenLimits = append(seenLimits, limit)
			}
			return true, 1000, 30, nil
		},
		runLongWindow: func(_ context.Context, _, _ string, _ time.Duration, _ int, limit int64, score int64, _ time.Time) (bool, int64, int, error) {
			return true, limit - score, 300, nil
		},
	}

	snapshot := AuthSnapshot{
		KeyID:             "key-2",
		AccountID:         "acc-2",
		AccountRatePolicy: &RatePolicy{RateLimitRPM: 1000, RateLimitTPM: 1000000, FreeTokenWeightTenths: 1},
		KeyRatePolicy:     &RatePolicy{RateLimitRPM: 5, RateLimitTPM: 500, FreeTokenWeightTenths: 1}, // key is tighter
	}
	tierLimits := TierLimits{RPM: 100, TPM: 8000}

	result, err := limiter.CheckWithTier(context.Background(), snapshot, "alias-x", TierVerified, tierLimits, 0, 50, 0)
	if err != nil {
		t.Fatalf("CheckWithTier: %v", err)
	}
	if !result.Allowed {
		t.Fatalf("expected allowed, got %#v", result)
	}
	// Tier scope must clamp to min(5,100)=5 for RPM and min(500,8000)=500 for TPM.
	if len(seenLimits) != 2 {
		t.Fatalf("expected 2 tier-scope calls (rpm+tpm), got %d limits=%v", len(seenLimits), seenLimits)
	}
	if seenLimits[0] != 5 || seenLimits[1] != 500 {
		t.Fatalf("expected tier scope min limits {5,500}, got %v", seenLimits)
	}
}

func TestCheckWithTierTierOverridesWinOverEnvDefaults(t *testing.T) {
	var tierRPMSeen int
	limiter := &Limiter{
		now: func() time.Time { return time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC) },
		runSlidingWindow: func(_ context.Context, keys []string, limit int, _ int64, _ time.Time) (bool, int, int, error) {
			if strings.Contains(keys[0], "rl:{tier:verified:acc-3}:rpm:current") {
				tierRPMSeen = limit
			}
			return true, 1000, 30, nil
		},
		runLongWindow: func(_ context.Context, _, _ string, _ time.Duration, _ int, limit int64, score int64, _ time.Time) (bool, int64, int, error) {
			return true, limit - score, 300, nil
		},
	}

	snapshot := AuthSnapshot{
		KeyID:             "key-3",
		AccountID:         "acc-3",
		AccountRatePolicy: &RatePolicy{RateLimitRPM: 1000, RateLimitTPM: 1000000, FreeTokenWeightTenths: 1},
		KeyRatePolicy: &RatePolicy{
			RateLimitRPM:          1000,
			RateLimitTPM:          1000000,
			FreeTokenWeightTenths: 1,
			TierOverrides: map[string]TierOverridePol{
				"verified": {RPM: 77, TPM: 9999},
			},
		},
	}
	envTier := TierLimits{RPM: 120, TPM: 8000}

	if _, err := limiter.CheckWithTier(context.Background(), snapshot, "alias-x", TierVerified, envTier, 0, 0, 0); err != nil {
		t.Fatalf("CheckWithTier: %v", err)
	}
	// Per-key override 77 must beat env default 120, then min(keyRPM=1000, 77)=77.
	if tierRPMSeen != 77 {
		t.Fatalf("expected tier override RPM 77, got %d", tierRPMSeen)
	}
}

func TestCheckWithTierShortCircuitsOnKeyDeny(t *testing.T) {
	var sawTier bool
	limiter := &Limiter{
		now: func() time.Time { return time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC) },
		runSlidingWindow: func(_ context.Context, keys []string, limit int, _ int64, _ time.Time) (bool, int, int, error) {
			if strings.Contains(keys[0], "rl:{tier:") {
				sawTier = true
			}
			// Deny on the key-scope RPM bucket.
			if strings.Contains(keys[0], "rl:{key:key-4:") {
				return false, 0, 30, nil
			}
			return true, limit - 1, 30, nil
		},
		runLongWindow: func(_ context.Context, _, _ string, _ time.Duration, _ int, limit int64, score int64, _ time.Time) (bool, int64, int, error) {
			return true, limit - score, 300, nil
		},
	}

	snapshot := AuthSnapshot{
		KeyID:             "key-4",
		AccountID:         "acc-4",
		AccountRatePolicy: &RatePolicy{RateLimitRPM: 1000, RateLimitTPM: 1000000, FreeTokenWeightTenths: 1},
		KeyRatePolicy:     &RatePolicy{RateLimitRPM: 60, RateLimitTPM: 4000, FreeTokenWeightTenths: 1},
	}
	tierLimits := TierLimits{RPM: 1000, TPM: 1000000}

	result, err := limiter.CheckWithTier(context.Background(), snapshot, "alias-x", TierGuest, tierLimits, 0, 50, 0)
	if err != nil {
		t.Fatalf("CheckWithTier: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected key-scope deny to short-circuit")
	}
	if sawTier {
		t.Fatal("tier bucket must not be touched when key bucket already denies")
	}
}

func TestCheckWithTierZeroTierLimitsAllowed(t *testing.T) {
	limiter := &Limiter{
		now: func() time.Time { return time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC) },
		runSlidingWindow: func(_ context.Context, _ []string, limit int, _ int64, _ time.Time) (bool, int, int, error) {
			return true, limit - 1, 30, nil
		},
		runLongWindow: func(_ context.Context, _, _ string, _ time.Duration, _ int, limit int64, score int64, _ time.Time) (bool, int64, int, error) {
			return true, limit - score, 300, nil
		},
	}
	snapshot := AuthSnapshot{
		KeyID:             "key-5",
		AccountID:         "acc-5",
		AccountRatePolicy: &RatePolicy{RateLimitRPM: 1000, RateLimitTPM: 1000000, FreeTokenWeightTenths: 1},
		KeyRatePolicy:     &RatePolicy{RateLimitRPM: 0, RateLimitTPM: 0, FreeTokenWeightTenths: 1},
	}
	result, err := limiter.CheckWithTier(context.Background(), snapshot, "x", TierVerified, TierLimits{}, 0, 0, 0)
	if err != nil {
		t.Fatalf("CheckWithTier: %v", err)
	}
	if !result.Allowed {
		t.Fatalf("expected allowed when no positive limits, got %#v", result)
	}
}
