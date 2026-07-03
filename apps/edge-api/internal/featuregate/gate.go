// Package featuregate resolves per-tenant feature flags lazily from the
// control-plane with a 30-second in-memory cache. Cache misses trigger a
// single HTTP fetch; fetch errors fail closed (all flags false).
//
// Decision: fail-closed on control-plane errors. A tenant whose flags cannot
// be fetched is denied access to the feature rather than silently permitted.
// This matches the existing rate-limiter and budget-gate posture in edge-api.
package featuregate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/cpauth"
	"golang.org/x/sync/singleflight"
)

// Feature is an opaque token identifying a gateable capability.
type Feature string

const (
	FeatureRAG    Feature = "rag"
	FeatureVoice  Feature = "voice"
	FeatureRelay  Feature = "relay"
	FeatureCowork Feature = "cowork"
	// FeatureSSO gates GoTrue-native SAML 2.0 and OIDC provider login (issue #237).
	// Set when any of ENABLE_SSO_SAML, ENABLE_SSO_GOOGLE, or ENABLE_SSO_MICROSOFT
	// is enabled for the tenant in control-plane.
	FeatureSSO Feature = "sso"
)

// FlagsResponse is the JSON body returned by the control-plane
// GET /internal/featuregate/{tenant_id} endpoint.
type FlagsResponse struct {
	RAGEnabled    bool `json:"rag_enabled"`
	VoiceEnabled  bool `json:"voice_enabled"`
	RelayEnabled  bool `json:"relay_enabled"`
	CoworkEnabled bool `json:"cowork_enabled"`
	// SSOEnabled is true when at least one SSO provider (SAML, Google OIDC, or
	// Microsoft OIDC) is enabled for the tenant.
	SSOEnabled bool `json:"sso_enabled"`
}

// isEnabled returns whether f is enabled for this response.
func (r FlagsResponse) isEnabled(f Feature) bool {
	switch f {
	case FeatureRAG:
		return r.RAGEnabled
	case FeatureVoice:
		return r.VoiceEnabled
	case FeatureRelay:
		return r.RelayEnabled
	case FeatureCowork:
		return r.CoworkEnabled
	case FeatureSSO:
		return r.SSOEnabled
	default:
		return false // ponytail: unknown feature = deny
	}
}

// Config holds Gate construction parameters.
type Config struct {
	// ControlPlaneURL is the base URL of the control-plane service,
	// e.g. "http://control-plane:8081".
	ControlPlaneURL string
	// TTL is how long a fetched result is cached per tenant.
	// The spec requires < 60 s revocation; default 30 s.
	TTL time.Duration
	// HTTPClient is optional; defaults to a 5 s timeout client.
	HTTPClient *http.Client
}

type entry struct {
	flags  FlagsResponse
	loaded time.Time
}

// Gate resolves feature flags with a short per-tenant cache.
type Gate struct {
	baseURL string
	ttl     time.Duration
	client  *http.Client

	mu    sync.RWMutex
	cache map[uuid.UUID]entry

	// group collapses concurrent cache misses for the same tenant into a
	// single upstream fetch (issue #253: cold-cache stampede).
	group singleflight.Group
}

// New constructs a Gate. TTL defaults to 30 s when zero.
func New(cfg Config) *Gate {
	ttl := cfg.TTL
	if ttl == 0 {
		ttl = 30 * time.Second
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &Gate{
		baseURL: strings.TrimRight(cfg.ControlPlaneURL, "/"),
		ttl:     ttl,
		client:  client,
		cache:   make(map[uuid.UUID]entry),
	}
}

// Fetch returns the flags for tenantID, using the cache when valid.
// On any upstream error it returns a zeroed FlagsResponse and a non-nil error
// (fail-closed: caller treats all flags as false).
func (g *Gate) Fetch(ctx context.Context, tenantID uuid.UUID) (FlagsResponse, error) {
	g.mu.RLock()
	e, ok := g.cache[tenantID]
	g.mu.RUnlock()
	if ok && time.Since(e.loaded) < g.ttl {
		return e.flags, nil
	}

	v, err, _ := g.group.Do(tenantID.String(), func() (any, error) {
		return g.refresh(ctx, tenantID)
	})
	if err != nil {
		return FlagsResponse{}, err
	}
	return v.(FlagsResponse), nil
}

func (g *Gate) refresh(ctx context.Context, tenantID uuid.UUID) (FlagsResponse, error) {
	url := fmt.Sprintf("%s/internal/featuregate/%s", g.baseURL, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return FlagsResponse{}, fmt.Errorf("featuregate: build request: %w", err)
	}
	cpauth.SetHeader(req)

	resp, err := g.client.Do(req)
	if err != nil {
		return FlagsResponse{}, fmt.Errorf("featuregate: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return FlagsResponse{}, fmt.Errorf("featuregate: upstream status %d", resp.StatusCode)
	}

	var flags FlagsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&flags); err != nil {
		return FlagsResponse{}, fmt.Errorf("featuregate: decode: %w", err)
	}

	g.mu.Lock()
	g.cache[tenantID] = entry{flags: flags, loaded: time.Now()}
	g.mu.Unlock()

	return flags, nil
}

// Require returns middleware that gates the next handler on feat being enabled
// for the authenticated tenant. Disabled or unauthenticated requests receive
// 403 with a provider-blind body (no feature name, no internal detail).
func (g *Gate) Require(feat Feature) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFrom(r.Context())
			if !ok || user == nil {
				writeDenied(w)
				return
			}

			flags, err := g.Fetch(r.Context(), user.TenantID)
			if err != nil || !flags.isEnabled(feat) {
				writeDenied(w)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeDenied emits a provider-blind 403. No feature name, no internal detail.
func writeDenied(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	// ponytail: static body — no feature name, no provider name.
	_, _ = w.Write([]byte(`{"error":{"code":"ACCESS_DENIED","message":"access denied","type":"FORBIDDEN"}}`))
}
