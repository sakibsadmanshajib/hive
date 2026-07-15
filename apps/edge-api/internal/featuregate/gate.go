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

// Feature identifies a gateable capability. Its string value is the
// tenant_setting_key it mirrors on the control-plane side (e.g. "ENABLE_RAG"),
// so a Feature constant is a compile-checked label over a Gates map key, not
// a separate enum requiring its own translation logic.
type Feature string

const (
	FeatureRAG    Feature = "ENABLE_RAG"
	FeatureVoice  Feature = "ENABLE_VOICE"
	FeatureRelay  Feature = "ENABLE_RELAY"
	FeatureCowork Feature = "ENABLE_COWORK"
	// SSO login (issue #237) is gated on any one of three provider keys being
	// enabled, not a single key — use Gate.RequireAny with these three rather
	// than a composite Feature constant.
	FeatureSSOGoogle    Feature = "ENABLE_SSO_GOOGLE"
	FeatureSSOMicrosoft Feature = "ENABLE_SSO_MICROSOFT"
	FeatureSSOSaml      Feature = "ENABLE_SSO_SAML"
)

// FlagsResponse is the JSON body returned by the control-plane
// GET /internal/featuregate/{tenant_id} endpoint: every gate key the
// control-plane knows about, mapped to its enabled state for the tenant.
//
// Data-model rework (issue #293): this replaced a hardcoded five-boolean
// struct. Gates is generic on purpose — a brand new gate key needs no change
// to this type, to isEnabled, or to Require; see gate_test.go
// TestRequire_NewGateKey_NoCodeChangeNeeded.
type FlagsResponse struct {
	Gates map[string]bool `json:"gates"`
}

// isEnabled returns whether f is enabled for this response. A missing key
// (nil map, or key absent) reads as false: Go's zero-value map lookup, no
// special-casing required.
func (r FlagsResponse) isEnabled(f Feature) bool {
	return r.Gates[string(f)]
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
		// Detach from the winning caller's context. singleflight runs this
		// closure once and shares its result with every waiter on the key; if
		// the winner's request is cancelled mid-flight (client disconnect), a
		// captured ctx would fail the shared refresh and hand the same error to
		// every waiter, causing a burst of spurious fail-closed 403s. The HTTP
		// client's own 5s timeout still bounds the fetch (issue #253 review).
		return g.refresh(context.WithoutCancel(ctx), tenantID)
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
	return g.requireFunc(func(flags FlagsResponse) bool {
		return flags.isEnabled(feat)
	})
}

// RequireAny returns middleware that gates the next handler on any one of
// feats being enabled (logical OR). Used where a capability has multiple
// backing keys, e.g. SSO login is enabled when any configured provider key
// (Google, Microsoft, SAML) is on for the tenant.
func (g *Gate) RequireAny(feats ...Feature) func(http.Handler) http.Handler {
	return g.requireFunc(func(flags FlagsResponse) bool {
		for _, f := range feats {
			if flags.isEnabled(f) {
				return true
			}
		}
		return false
	})
}

// requireFunc is the shared gate mechanism behind Require and RequireAny:
// fetch the tenant's flags, deny on fetch error or unauthenticated request,
// otherwise defer the allow/deny decision to allowed.
func (g *Gate) requireFunc(allowed func(FlagsResponse) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFrom(r.Context())
			if !ok || user == nil {
				writeDenied(w)
				return
			}

			flags, err := g.Fetch(r.Context(), user.TenantID)
			if err != nil || !allowed(flags) {
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
