package authz

import (
	"context"
	"os"
	"strconv"
	"strings"
)

// Tier enumerates the four hot-path tiers Phase 12 ships with. Phase 20 will
// extend the resolver body to read Supabase verification state, but the
// enumeration stays stable so chat-app + api consumers are unaffected.
type Tier string

const (
	TierGuest      Tier = "guest"
	TierUnverified Tier = "unverified"
	TierVerified   Tier = "verified"
	TierCredited   Tier = "credited"
)

// TierLimits is the per-tier RPM/TPM pair. Zero in either dimension means
// "unlimited at the tier layer" — the per-key limit still applies.
type TierLimits struct {
	RPM int
	TPM int
}

// TierResolver maps a request context to a Tier and returns the env-driven
// default limits for that tier. Override values supplied by the per-key
// tier_overrides JSONB take precedence at enforcement time and are merged at
// the limiter call site, not here.
//
// Phase 12 stub: Resolve() reads the JWT claim hive_tier when present, else
// falls back to HIVE_TIER_DEFAULT (default "unverified"). Phase 20 will
// replace the body with a Supabase email/phone-verified state lookup.
type TierResolver struct {
	defaults map[Tier]TierLimits
	fallback Tier
}

// NewTierResolverFromEnv constructs a resolver whose default limits come from
// HIVE_TIER_LIMITS_<TIER>_RPM / _TPM env vars. Missing vars use the v1.1
// master-plan §Tier Model placeholder defaults documented in PLAN.md.
func NewTierResolverFromEnv() *TierResolver {
	defaults := map[Tier]TierLimits{
		TierGuest:      {RPM: envInt("HIVE_TIER_LIMITS_GUEST_RPM", 10), TPM: envInt("HIVE_TIER_LIMITS_GUEST_TPM", 2000)},
		TierUnverified: {RPM: envInt("HIVE_TIER_LIMITS_UNVERIFIED_RPM", 30), TPM: envInt("HIVE_TIER_LIMITS_UNVERIFIED_TPM", 4000)},
		TierVerified:   {RPM: envInt("HIVE_TIER_LIMITS_VERIFIED_RPM", 120), TPM: envInt("HIVE_TIER_LIMITS_VERIFIED_TPM", 8000)},
		TierCredited:   {RPM: envInt("HIVE_TIER_LIMITS_CREDITED_RPM", 600), TPM: envInt("HIVE_TIER_LIMITS_CREDITED_TPM", 20000)},
	}

	fallback := TierUnverified
	if raw := strings.ToLower(strings.TrimSpace(os.Getenv("HIVE_TIER_DEFAULT"))); raw != "" {
		if t, ok := parseTier(raw); ok {
			fallback = t
		}
	}
	return &TierResolver{defaults: defaults, fallback: fallback}
}

// NewTierResolverWithDefaults is a constructor convenient for tests — bypasses env.
func NewTierResolverWithDefaults(defaults map[Tier]TierLimits, fallback Tier) *TierResolver {
	return &TierResolver{defaults: defaults, fallback: fallback}
}

// tierClaimKey is the context key under which authn middleware stores the
// JWT-claimed tier. Phase 20 will populate this from Supabase. Phase 12 ships
// the seam; tests inject directly via WithTierClaim.
type tierClaimKey struct{}

// WithTierClaim returns a context with the supplied tier set as the JWT claim
// override. Used by tests and (in Phase 20) by Supabase auth middleware.
func WithTierClaim(ctx context.Context, t Tier) context.Context {
	return context.WithValue(ctx, tierClaimKey{}, t)
}

// Resolve returns the Tier for the request. JWT claim wins; env fallback otherwise.
func (r *TierResolver) Resolve(ctx context.Context) Tier {
	if r == nil {
		return TierUnverified
	}
	if v, ok := ctx.Value(tierClaimKey{}).(Tier); ok {
		if _, valid := r.defaults[v]; valid {
			return v
		}
	}
	return r.fallback
}

// Limits returns the env-driven default limits for a tier.
func (r *TierResolver) Limits(t Tier) TierLimits {
	if r == nil {
		return TierLimits{}
	}
	return r.defaults[t]
}

// EffectiveLimits merges env defaults with optional per-key overrides for the
// supplied tier. Override fields equal to zero mean "no override; keep env value".
// Returned limits are the binding tier-layer value the limiter compares against.
func (r *TierResolver) EffectiveLimits(t Tier, overrideRPM, overrideTPM int) TierLimits {
	base := r.Limits(t)
	if overrideRPM > 0 {
		base.RPM = overrideRPM
	}
	if overrideTPM > 0 {
		base.TPM = overrideTPM
	}
	return base
}

// MinPositive returns the smaller of two positive integers. If one side is
// non-positive (zero or negative — meaning "unlimited at that layer"), the
// other side wins. Used to compute min(keyLimit, tierLimit) per dimension.
func MinPositive(a, b int) int {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

func parseTier(raw string) (Tier, bool) {
	switch Tier(raw) {
	case TierGuest, TierUnverified, TierVerified, TierCredited:
		return Tier(raw), true
	}
	return "", false
}

func envInt(name string, def int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return def
	}
	return v
}
