package apikeys

import "testing"

// Owner directive, 2026-08-30: no default rate limit anywhere. A rate limit
// exists only where someone explicitly configured one.
//
// defaultRatePolicy is what an account or key with no api_key_rate_policies /
// account_rate_policies row gets. It shipped as 60 requests and 120000 tokens
// per minute, a pair nobody chose, and the edge limiter enforces whatever it
// returns. It is also what the owner UI displays as "the baseline", so a
// number here is a number a customer sees.
func TestDefaultRatePolicyImposesNoLimit(t *testing.T) {
	got := defaultRatePolicy()

	if got.RateLimitRPM != 0 {
		t.Errorf("defaultRatePolicy().RateLimitRPM = %d, want 0", got.RateLimitRPM)
	}
	if got.RateLimitTPM != 0 {
		t.Errorf("defaultRatePolicy().RateLimitTPM = %d, want 0", got.RateLimitTPM)
	}
	if got.RollingFiveHourLimit != 0 {
		t.Errorf("defaultRatePolicy().RollingFiveHourLimit = %d, want 0", got.RollingFiveHourLimit)
	}
	if got.WeeklyLimit != 0 {
		t.Errorf("defaultRatePolicy().WeeklyLimit = %d, want 0", got.WeeklyLimit)
	}

	// FreeTokenWeightTenths is a WEIGHT, not a limit: it scales how a free
	// token counts toward a fraud window that is itself off by default. Zeroing
	// it would make every request score zero and quietly neuter any window an
	// operator later switches on, so it stays at 1.
	if got.FreeTokenWeightTenths != 1 {
		t.Errorf("defaultRatePolicy().FreeTokenWeightTenths = %d, want 1; it is a weight, not a limit", got.FreeTokenWeightTenths)
	}
}

// TestTheRemovedDefaultsAreStillConfigurableGuard is a GUARD, and is named so
// after review flagged the previous version as near-vacuous: asserting only
// that two constants are positive is a test that essentially cannot fail.
//
// Strengthened to assert the thing that actually matters, which is that
// removing the defaults did not remove the ability to set the very values that
// were removed. An operator who wants the old behaviour back must be able to
// type 60 and 120000 into the console and have them accepted, so the validation
// ceilings have to sit above them. That is a real relationship between two
// parts of the change rather than a positivity check.
func TestTheRemovedDefaultsAreStillConfigurableGuard(t *testing.T) {
	const (
		removedRPM = 60
		removedTPM = 120000
	)

	if RateLimitRPMMax < removedRPM {
		t.Errorf("RateLimitRPMMax = %d, below the removed default of %d; an operator could no longer restore it", RateLimitRPMMax, removedRPM)
	}
	if RateLimitTPMMax < removedTPM {
		t.Errorf("RateLimitTPMMax = %d, below the removed default of %d", RateLimitTPMMax, removedTPM)
	}

	// The range UpdateLimits validates against is [0, Max]. Zero has to be
	// inside it, or the new default would be a value the console rejects and
	// every key would be unwritable through the owner UI.
	if got := defaultRatePolicy().RateLimitRPM; got < 0 || got > RateLimitRPMMax {
		t.Errorf("defaultRatePolicy RPM %d is outside the accepted range [0, %d]", got, RateLimitRPMMax)
	}
	if got := defaultRatePolicy().RateLimitTPM; got < 0 || got > RateLimitTPMMax {
		t.Errorf("defaultRatePolicy TPM %d is outside the accepted range [0, %d]", got, RateLimitTPMMax)
	}
}
