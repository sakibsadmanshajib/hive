package authz

import (
	"context"
	"fmt"
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

// NewTierResolverFromEnv constructs a resolver whose limits come from
// HIVE_TIER_LIMITS_<TIER>_RPM / _TPM env vars, and REFUSES rather than falls
// back when one of them is set to something it cannot use.
//
// A missing var means NO LIMIT. Owner directive 2026-08-30: Hive imposes no
// default rate limit anywhere, and a rate limit exists only where someone
// explicitly configured one.
//
// THIS LAYER IS CURRENTLY UNWIRED, and that has to be said here because the
// first version of this comment claimed the opposite. Nothing outside tests
// calls this constructor, and nothing outside tests calls Limiter.CheckWithTier
// either; the only limiter call on the live path is authorizer.go's
// Limiter.Check, which uses the account and key policies carried on the auth
// snapshot. So the placeholder pairs removed here (guest 10/2000, unverified
// 30/4000, verified 120/8000, credited 600/20000) were never enforced on any
// request. Removing them is directionally right and inert. The default that
// genuinely was live is defaultRatePolicy in control-plane's apikeys
// repository, at 60 requests and 120000 tokens per minute.
//
// Why an error return rather than a log line. These variables are read exactly
// once at construction and never on the request path, so there is no
// availability argument for limping on. Zero now means unlimited, so silently
// falling back on an unparseable value would delete the limit an operator was
// trying to set, and a boot log nothing alerts on is a weak guard against that.
// Refusing hands the decision to whoever wires this layer up, which is the only
// place with the context to choose between failing the boot and degrading.
func NewTierResolverFromEnv() (*TierResolver, error) {
	defaults := map[Tier]TierLimits{}
	for tier, prefix := range map[Tier]string{
		TierGuest:      "HIVE_TIER_LIMITS_GUEST",
		TierUnverified: "HIVE_TIER_LIMITS_UNVERIFIED",
		TierVerified:   "HIVE_TIER_LIMITS_VERIFIED",
		TierCredited:   "HIVE_TIER_LIMITS_CREDITED",
	} {
		rpm, err := envInt(prefix + "_RPM")
		if err != nil {
			return nil, err
		}
		tpm, err := envInt(prefix + "_TPM")
		if err != nil {
			return nil, err
		}
		defaults[tier] = TierLimits{RPM: rpm, TPM: tpm}
	}

	fallback := TierUnverified
	if raw := strings.ToLower(strings.TrimSpace(os.Getenv("HIVE_TIER_DEFAULT"))); raw != "" {
		if t, ok := parseTier(raw); ok {
			fallback = t
		}
	}
	return &TierResolver{defaults: defaults, fallback: fallback}, nil
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

// envInt reads a non-negative integer from the environment. Unset, or set to
// whitespace only, means 0, which means no limit at this layer.
//
// A value that IS set and cannot be used is an ERROR, never a fallback. Zeroing
// the tier defaults reversed the failure direction: HIVE_TIER_LIMITS_GUEST_RPM=ten
// or a negative value used to fall back to a placeholder LIMIT, so a
// misconfiguration still limited something. Falling back now would mean
// unlimited, which silently deletes the limit an operator was trying to set.
// Unlimited by accident is the dangerous direction, so this refuses.
//
// The rejected value reported is the TRIMMED one that was actually parsed, not
// the raw environment value, so the message names what failed rather than a
// string that differs from it by whitespace.
func envInt(name string) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("authz: %s = %q is not an integer; refusing rather than treating the limit as unset, which would mean unlimited", name, raw)
	}
	if v < 0 {
		return 0, fmt.Errorf("authz: %s = %d is negative; refusing rather than treating the limit as unset, which would mean unlimited", name, v)
	}
	return v, nil
}
