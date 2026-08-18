// Package auth provides Supabase JWT validation and request user context
// for the edge-api. The validator caches the JWKS upstream so each request
// only does a constant-time signature check; tokens are checked for
// issuer, audience, and expiration before claims are returned.
package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
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
	// CAFile optionally names a PEM file holding the certificate authority
	// to trust when fetching JWKSURL. When set it REPLACES the system
	// roots for that one fetch, so the named authority is the only one
	// that can vouch for the JWKS host. It exists for the self-hosted
	// (enterprise) deployment, where the JWKS is served by an in-stack TLS
	// terminator on a compose service name, holding a private CA's
	// certificate that no public authority could issue. Leave it empty on
	// deployments whose JWKS host presents a publicly trusted certificate.
	//
	// This never weakens the https-only rule enforced at the caller: the
	// transport still requires TLS and still verifies the chain and the
	// hostname. It narrows which authority is acceptable, it does not skip
	// verification.
	CAFile string
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
	registerOpts := []jwk.RegisterOption{jwk.WithRefreshInterval(cfg.JWKSTTL)}
	if cfg.CAFile != "" {
		client, err := httpClientTrusting(cfg.CAFile)
		if err != nil {
			return nil, err
		}
		registerOpts = append(registerOpts, jwk.WithHTTPClient(client))
	}
	if err := cache.Register(cfg.JWKSURL, registerOpts...); err != nil {
		return nil, fmt.Errorf("auth: jwks register: %w", err)
	}
	if _, err := cache.Refresh(ctx, cfg.JWKSURL); err != nil {
		return nil, fmt.Errorf("auth: jwks initial refresh: %w", err)
	}
	return &SupabaseJWTValidator{cfg: cfg, cache: cache}, nil
}

// httpClientTrusting returns an HTTP client that trusts exactly one
// certificate authority: whatever the PEM file at path holds. The system
// roots are deliberately NOT included.
//
// Naming a CA file is a statement that the JWKS is served by a specific,
// operator-controlled authority, which for this deployment is an in-stack TLS
// terminator on a compose service name that no public authority could ever
// issue for. Keeping the public roots in the pool alongside it would leave
// every public CA able to vouch for that fetch for no benefit, so the file
// replaces the trust set rather than extending it. A deployment whose JWKS
// host presents a publicly trusted certificate simply leaves the variable
// unset and gets the system pool, which is the default path.
//
// An unreadable file, or one carrying no certificate, is fatal rather than a
// silent fall back to the system pool: a deployment that asked for a private
// CA and quietly did not get one would fail later, at the first token, with a
// far worse error.
func httpClientTrusting(path string) (*http.Client, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("auth: read jwks ca file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("auth: jwks ca file %q holds no certificate", path)
	}
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    pool,
				MinVersion: tls.VersionTLS12,
			},
		},
	}, nil
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
