package authz

import (
	"context"
	"testing"
)

func TestTierResolverFallbackFromEnvDefault(t *testing.T) {
	t.Setenv("HIVE_TIER_DEFAULT", "guest")
	r := NewTierResolverFromEnv()
	if got := r.Resolve(context.Background()); got != TierGuest {
		t.Fatalf("expected guest fallback, got %s", got)
	}
}

func TestTierResolverJWTClaimWins(t *testing.T) {
	t.Setenv("HIVE_TIER_DEFAULT", "guest")
	r := NewTierResolverFromEnv()
	ctx := WithTierClaim(context.Background(), TierVerified)
	if got := r.Resolve(ctx); got != TierVerified {
		t.Fatalf("expected verified from JWT claim, got %s", got)
	}
}

func TestTierResolverInvalidClaimFallsBack(t *testing.T) {
	t.Setenv("HIVE_TIER_DEFAULT", "credited")
	r := NewTierResolverFromEnv()
	ctx := context.WithValue(context.Background(), tierClaimKey{}, Tier("platinum"))
	if got := r.Resolve(ctx); got != TierCredited {
		t.Fatalf("expected credited fallback on invalid claim, got %s", got)
	}
}

func TestTierResolverEnvDrivenLimits(t *testing.T) {
	t.Setenv("HIVE_TIER_LIMITS_VERIFIED_RPM", "999")
	t.Setenv("HIVE_TIER_LIMITS_VERIFIED_TPM", "12345")
	r := NewTierResolverFromEnv()
	limits := r.Limits(TierVerified)
	if limits.RPM != 999 || limits.TPM != 12345 {
		t.Fatalf("expected env-driven verified limits, got %#v", limits)
	}
}

func TestEffectiveLimitsOverridesPositiveValues(t *testing.T) {
	r := NewTierResolverWithDefaults(map[Tier]TierLimits{
		TierVerified: {RPM: 100, TPM: 1000},
	}, TierVerified)
	got := r.EffectiveLimits(TierVerified, 50, 0)
	if got.RPM != 50 || got.TPM != 1000 {
		t.Fatalf("expected RPM override with TPM kept, got %#v", got)
	}
	got = r.EffectiveLimits(TierVerified, 0, 0)
	if got.RPM != 100 || got.TPM != 1000 {
		t.Fatalf("expected env defaults when no override, got %#v", got)
	}
}

func TestMinPositive(t *testing.T) {
	cases := []struct {
		a, b, want int
	}{
		{60, 100, 60},
		{100, 60, 60},
		{0, 60, 60},
		{60, 0, 60},
		{0, 0, 0},
		{-1, 5, 5},
	}
	for _, tc := range cases {
		if got := MinPositive(tc.a, tc.b); got != tc.want {
			t.Fatalf("MinPositive(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
