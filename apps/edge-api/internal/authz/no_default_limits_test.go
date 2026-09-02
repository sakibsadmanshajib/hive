package authz

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/packages/ratewindows"
)

// Owner directive, 2026-08-30: Hive imposes NO default rate limit anywhere. A
// rate limit exists only where someone explicitly configured one.
//
// SCOPE CORRECTION, after independent review traced the call graph rather than
// the description. The tier layer removed here is UNWIRED: nothing outside
// tests calls NewTierResolverFromEnv, and nothing outside tests calls
// Limiter.CheckWithTier. The only limiter call on the live request path is
// authorizer.go's Limiter.Check, which uses the account and key policies on the
// auth snapshot. So the placeholder pairs deleted here (guest 10/2000,
// unverified 30/4000, verified 120/8000, credited 600/20000) were never
// enforced on any request, and an earlier version of this comment claiming a
// live 30/4000 cap was wrong.
//
// The default in this change that genuinely WAS live is defaultRatePolicy in
// control-plane's apikeys repository, at 60 requests and 120000 tokens per
// minute, which reaches the edge through the auth snapshot and then checkScope.
// Its guards live in apps/control-plane/internal/apikeys.
//
// These tests still earn their place: they stop the placeholders being
// reintroduced, and they pin the failure direction of the parser, which is what
// will matter on the day this layer is wired up.

func TestTierDefaultsImposeNoLimitWhenUnconfigured(t *testing.T) {
	for _, name := range []string{
		"HIVE_TIER_LIMITS_GUEST_RPM", "HIVE_TIER_LIMITS_GUEST_TPM",
		"HIVE_TIER_LIMITS_UNVERIFIED_RPM", "HIVE_TIER_LIMITS_UNVERIFIED_TPM",
		"HIVE_TIER_LIMITS_VERIFIED_RPM", "HIVE_TIER_LIMITS_VERIFIED_TPM",
		"HIVE_TIER_LIMITS_CREDITED_RPM", "HIVE_TIER_LIMITS_CREDITED_TPM",
	} {
		t.Setenv(name, "")
	}

	r, err := NewTierResolverFromEnv()
	if err != nil {
		t.Fatalf("NewTierResolverFromEnv with nothing configured: %v", err)
	}
	for _, tier := range []Tier{TierGuest, TierUnverified, TierVerified, TierCredited} {
		got := r.Limits(tier)
		if got.RPM != 0 || got.TPM != 0 {
			t.Errorf("tier %s unconfigured limits = %#v, want {0 0}; a limit nobody configured must not exist", tier, got)
		}
	}
}

// The knob still works. Removing the defaults must not remove the ability to
// set a limit deliberately.
func TestTierLimitsStillHonourExplicitConfiguration(t *testing.T) {
	t.Setenv("HIVE_TIER_LIMITS_GUEST_RPM", "7")
	t.Setenv("HIVE_TIER_LIMITS_GUEST_TPM", "700")

	r, err := NewTierResolverFromEnv()
	if err != nil {
		t.Fatalf("NewTierResolverFromEnv: %v", err)
	}
	got := r.Limits(TierGuest)
	if got.RPM != 7 || got.TPM != 700 {
		t.Fatalf("explicitly configured guest limits = %#v, want {7 700}", got)
	}
}

// The failure direction, which reversed when the defaults went to zero.
//
// Before: a typo (`ten`) or a negative value fell back to a placeholder LIMIT,
// so a misconfiguration still limited something. Zero now means unlimited, so
// the same typo would silently remove the limit the operator was setting.
// Unlimited by accident is the dangerous direction, so construction refuses.
func TestUnusableTierLimitValueIsRefusedNotSilentlyUnlimited(t *testing.T) {
	cases := map[string]string{
		"not a number":  "ten",
		"negative":      "-5",
		"trailing junk": "60rpm",
		"float":         "60.5",
		"empty quotes":  `""`,
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HIVE_TIER_LIMITS_GUEST_RPM", raw)

			r, err := NewTierResolverFromEnv()
			if err == nil {
				t.Fatalf("value %q was accepted and produced limits %#v; a value that was set and cannot be parsed must not resolve to unlimited", raw, r.Limits(TierGuest))
			}
			if r != nil {
				t.Errorf("a refused construction returned a non-nil resolver, which a caller could still use")
			}
			if !strings.Contains(err.Error(), "HIVE_TIER_LIMITS_GUEST_RPM") {
				t.Errorf("error %q does not name the variable, so an operator cannot find which one is wrong", err)
			}
			if !strings.Contains(err.Error(), strings.TrimSpace(raw)) {
				t.Errorf("error %q does not quote the rejected value %q", err, raw)
			}
		})
	}
}

// Whitespace-only is indistinguishable from unset and must stay accepted, or
// an operator who leaves `HIVE_TIER_LIMITS_GUEST_RPM= ` in a file cannot boot.
func TestWhitespaceOnlyTierLimitIsTreatedAsUnset(t *testing.T) {
	t.Setenv("HIVE_TIER_LIMITS_GUEST_RPM", "   ")

	r, err := NewTierResolverFromEnv()
	if err != nil {
		t.Fatalf("whitespace-only value was refused: %v", err)
	}
	if got := r.Limits(TierGuest).RPM; got != 0 {
		t.Errorf("guest RPM = %d, want 0", got)
	}
}

// With nothing configured the limiter must not merely allow the request, it
// must not RUN. That is what makes the fail-closed branch in authorizer.go
// unreachable by default rather than merely unlikely: a limiter that never
// consults Redis cannot be degraded, so a Redis outage stops manufacturing
// 429s for accounts that carry no limits at all.
func TestCheckWithNoConfiguredLimitsNeverRunsAWindow(t *testing.T) {
	windowCalls := 0
	longCalls := 0
	l := &Limiter{
		now: time.Now,
		runSlidingWindow: func(context.Context, []string, int, int64, time.Time) (bool, int, int, error) {
			windowCalls++
			return false, 0, 0, errors.New("sliding window must not have been consulted")
		},
		runLongWindow: func(context.Context, string, ratewindows.Shape, time.Time, int64, int64, time.Time) (longWindowResult, error) {
			longCalls++
			return longWindowResult{}, errors.New("long window must not have been consulted")
		},
	}

	unlimited := &RatePolicy{FreeTokenWeightTenths: 1}
	snapshot := AuthSnapshot{
		AccountID:         "acct-1",
		KeyID:             "key-1",
		AccountRatePolicy: unlimited,
		KeyRatePolicy:     unlimited,
	}

	result, err := l.CheckWithTier(context.Background(), snapshot, "hive-free", TierUnverified, TierLimits{}, 0, 1_000_000, 0)
	if err != nil {
		t.Fatalf("CheckWithTier with no configured limits: %v", err)
	}
	if !result.Allowed {
		t.Fatalf("result = %#v, want allowed; no limit was configured anywhere", result)
	}
	if windowCalls != 0 || longCalls != 0 {
		t.Fatalf("limiter ran %d sliding and %d long windows with nothing configured, want 0 and 0", windowCalls, longCalls)
	}
}
