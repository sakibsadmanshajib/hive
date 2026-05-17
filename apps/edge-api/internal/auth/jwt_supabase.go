// Package auth provides Supabase JWT validation and request user context
// for the edge-api. The validator caches the JWKS upstream so each request
// only does a constant-time signature check; tokens are checked for
// issuer, audience, and expiration before claims are returned.
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// ErrJWTExpired is returned by Parse when the token's exp claim is in the
// past (within the configured clock skew tolerance).
var ErrJWTExpired = errors.New("auth: jwt expired")

// errJWTExpired caches jwt.ErrTokenExpired() at package init so the hot
// path's errors.Is comparison does not call the constructor on every
// request. jwx exposes the sentinel as a function for API-stability
// reasons; the underlying error value is fixed.
var errJWTExpired = jwt.ErrTokenExpired()

// SupabaseJWTConfig configures the validator. Issuer and JWKSURL are
// required. JWTAudience defaults to "authenticated" (the Supabase default),
// and JWKSTTL defaults to 24h.
type SupabaseJWTConfig struct {
	Issuer      string
	JWTAudience string
	JWKSURL     string
	JWKSTTL     time.Duration
	// ClockSkew tolerates small clock drift between this process and the
	// token issuer. Defaults to 30s when zero.
	ClockSkew time.Duration
}

// Claims holds the subset of token claims the edge-api consumes downstream.
type Claims struct {
	Sub      uuid.UUID
	Email    string
	TenantID uuid.UUID
	Role     string
	Tenants  []TenantMembership
}

// TenantMembership describes a single tenant scope on a multi-tenant claim.
type TenantMembership struct {
	ID   uuid.UUID
	Role string
}

// SupabaseJWTValidator validates Supabase-issued RS256/ES256 tokens against
// a cached JWKS endpoint.
type SupabaseJWTValidator struct {
	cfg   SupabaseJWTConfig
	cache *jwk.Cache
}

// NewSupabaseJWTValidator constructs a validator and performs an initial
// JWKS fetch. The validator refreshes the JWKS on the configured TTL.
func NewSupabaseJWTValidator(ctx context.Context, cfg SupabaseJWTConfig) (*SupabaseJWTValidator, error) {
	if cfg.Issuer == "" || cfg.JWKSURL == "" {
		return nil, errors.New("auth: SupabaseJWTConfig.Issuer and JWKSURL required")
	}
	if cfg.JWTAudience == "" {
		cfg.JWTAudience = "authenticated"
	}
	if cfg.JWKSTTL == 0 {
		cfg.JWKSTTL = 24 * time.Hour
	}
	if cfg.ClockSkew == 0 {
		cfg.ClockSkew = 30 * time.Second
	}
	cache := jwk.NewCache(ctx)
	if err := cache.Register(cfg.JWKSURL, jwk.WithRefreshInterval(cfg.JWKSTTL)); err != nil {
		return nil, fmt.Errorf("auth: jwks register: %w", err)
	}
	if _, err := cache.Refresh(ctx, cfg.JWKSURL); err != nil {
		return nil, fmt.Errorf("auth: jwks initial refresh: %w", err)
	}
	return &SupabaseJWTValidator{cfg: cfg, cache: cache}, nil
}

// Parse validates the token signature, issuer, audience, and expiration,
// then extracts edge-api claims into a Claims struct.
func (v *SupabaseJWTValidator) Parse(ctx context.Context, raw string) (Claims, error) {
	set, err := v.cache.Get(ctx, v.cfg.JWKSURL)
	if err != nil {
		return Claims{}, fmt.Errorf("auth: jwks fetch: %w", err)
	}
	tok, err := jwt.Parse([]byte(raw),
		jwt.WithKeySet(set),
		jwt.WithIssuer(v.cfg.Issuer),
		jwt.WithAudience(v.cfg.JWTAudience),
		jwt.WithAcceptableSkew(v.cfg.ClockSkew),
	)
	if err != nil {
		if errors.Is(err, errJWTExpired) {
			return Claims{}, ErrJWTExpired
		}
		return Claims{}, err
	}

	out := Claims{}
	if sub := tok.Subject(); sub != "" {
		if id, err := uuid.Parse(sub); err == nil {
			out.Sub = id
		}
	}
	if val, ok := tok.Get("email"); ok {
		if s, _ := val.(string); s != "" {
			out.Email = s
		}
	}
	if val, ok := tok.Get("tenant_id"); ok {
		if s, _ := val.(string); s != "" {
			if id, err := uuid.Parse(s); err == nil {
				out.TenantID = id
			}
		}
	}
	// Roles are emitted by the control-plane in either case
	// (legacy "OWNER"/"ADMIN" vs Phase 19 "owner"/"admin"). The
	// authz policy table is keyed lowercase, so normalize here once
	// at the trust boundary instead of forcing every downstream
	// caller to remember.
	if val, ok := tok.Get("role"); ok {
		raw, _ := val.(string)
		out.Role = strings.ToLower(raw)
	}
	if val, ok := tok.Get("tenants"); ok {
		if arr, _ := val.([]any); arr != nil {
			for _, e := range arr {
				m, _ := e.(map[string]any)
				idS, _ := m["id"].(string)
				roleS, _ := m["role"].(string)
				if id, err := uuid.Parse(idS); err == nil {
					out.Tenants = append(out.Tenants, TenantMembership{
						ID:   id,
						Role: strings.ToLower(roleS),
					})
				}
			}
		}
	}
	return out, nil
}
