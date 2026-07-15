package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Resolver is the single sanctioned API for reading tenant settings.
// Direct queries against the table are blocked by lint.
type Resolver struct {
	pool *pgxpool.Pool
	ttl  time.Duration

	mu    sync.RWMutex
	cache map[uuid.UUID]map[Key]entry
}

type entry struct {
	enabled bool
	value   json.RawMessage
	loaded  time.Time
}

func NewResolver(pool *pgxpool.Pool, ttl time.Duration) *Resolver {
	return &Resolver{
		pool:  pool,
		ttl:   ttl,
		cache: make(map[uuid.UUID]map[Key]entry),
	}
}

// IsEnabled returns true only when the row exists and enabled = true.
// An unset key returns false; callers that need to distinguish "off" from
// "unset" should use ValueRaw.
func (r *Resolver) IsEnabled(ctx context.Context, tenantID uuid.UUID, key Key) bool {
	e, ok := r.lookup(ctx, tenantID, key)
	if !ok {
		return false
	}
	return e.enabled
}

// ValueRaw returns the value_json column if the row exists.
func (r *Resolver) ValueRaw(ctx context.Context, tenantID uuid.UUID, key Key) (json.RawMessage, bool) {
	e, ok := r.lookup(ctx, tenantID, key)
	if !ok {
		return nil, false
	}
	return e.value, true
}

func (r *Resolver) lookup(ctx context.Context, tenantID uuid.UUID, key Key) (entry, bool) {
	r.mu.RLock()
	if perTenant, ok := r.cache[tenantID]; ok {
		if e, ok := perTenant[key]; ok && time.Since(e.loaded) < r.ttl {
			r.mu.RUnlock()
			return e, true
		}
	}
	r.mu.RUnlock()
	return r.refresh(ctx, tenantID, key)
}

func (r *Resolver) refresh(ctx context.Context, tenantID uuid.UUID, key Key) (entry, bool) {
	var e entry
	err := r.pool.QueryRow(ctx,
		`SELECT enabled, COALESCE(value_json, 'null'::jsonb)
		   FROM public.tenant_settings
		  WHERE tenant_id = $1 AND key = $2::public.tenant_setting_key`,
		tenantID, string(key)).Scan(&e.enabled, &e.value)
	if errors.Is(err, pgx.ErrNoRows) {
		return entry{}, false
	}
	if err != nil {
		return entry{}, false
	}
	e.loaded = time.Now()
	r.mu.Lock()
	per, ok := r.cache[tenantID]
	if !ok {
		per = make(map[Key]entry, 4)
		r.cache[tenantID] = per
	}
	per[key] = e
	r.mu.Unlock()
	return e, true
}

// clientVisibleGateCategories is the allowlist of feature_gate_keys.category
// values that are safe to expose to an authenticated end user or OWUI Function
// (issue #293 security review). It covers only capability gates a client needs
// to adapt its own UI: carl (the RAG/voice/relay/cowork feature keys) and sso.
// The admin, billing, and audit_sink categories are deliberately excluded:
// exposing them would leak the deployment's commercial and operational posture
// to any authenticated user, the same information-disclosure class the
// 20260715_04 migration avoided by not granting feature_gate_keys to the
// authenticated role. Fail-closed: a newly added category is excluded here
// until it is explicitly listed, so a new admin/billing key never leaks by
// default while a new carl/sso key is exposed automatically.
var clientVisibleGateCategories = []string{"carl", "sso"}

// AllEnabled resolves every gate key registered in public.feature_gate_keys
// for tenantID in a single query, returning enabled=false for any key with no
// tenant_settings row. It is the FULL set across all categories and is for
// internal/admin callers (e.g. the admin console toggle UI) that must see
// every gate regardless of category. The client-facing featuregate endpoint
// uses ClientVisibleEnabled instead, so admin/billing/audit_sink gates never
// reach an end user; do not swap this method into that path (issue #293).
//
// Unlike IsEnabled, this bypasses the per-key in-memory cache: it always
// issues one fresh, indexed (tenant_id) query.
func (r *Resolver) AllEnabled(ctx context.Context, tenantID uuid.UUID) (map[Key]bool, error) {
	return r.gateMap(ctx, tenantID, nil)
}

// ClientVisibleEnabled resolves only the gate keys whose
// feature_gate_keys.category is in clientVisibleGateCategories (carl, sso):
// the subset safe to expose to an authenticated end user or OWUI Function
// (issue #293 security review). It backs the control-plane featuregate
// endpoint, which feeds edge-api's Gate/Require and the /v1/featuregate read
// surface, so admin, billing, and audit_sink gates never leave control-plane.
// Fail-closed: a new category is excluded until added to the allowlist, so a
// new admin/billing key never leaks by default while a new carl/sso key is
// exposed automatically.
func (r *Resolver) ClientVisibleEnabled(ctx context.Context, tenantID uuid.UUID) (map[Key]bool, error) {
	return r.gateMap(ctx, tenantID, clientVisibleGateCategories)
}

// gateMap resolves feature_gate_keys left-joined against tenant_settings for
// tenantID. When categories is non-nil the result is restricted to those
// feature_gate_keys.category values; nil returns every registered key. It
// always issues one fresh, indexed (tenant_id) query and bypasses the per-key
// cache (the edge-api Gate already caches the whole response per tenant for
// 30s with singleflight dedup on cold misses).
func (r *Resolver) gateMap(ctx context.Context, tenantID uuid.UUID, categories []string) (map[Key]bool, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT k.key, COALESCE(s.enabled, false) AS enabled
		  FROM public.feature_gate_keys k
		  LEFT JOIN public.tenant_settings s
		    ON s.tenant_id = $1 AND s.key = k.key
		 WHERE $2::text[] IS NULL OR k.category = ANY($2::text[])`,
		tenantID, categories)
	if err != nil {
		return nil, fmt.Errorf("settings: query feature gate keys: %w", err)
	}
	defer rows.Close()

	out := make(map[Key]bool)
	for rows.Next() {
		var key Key
		var enabled bool
		if err := rows.Scan(&key, &enabled); err != nil {
			return nil, fmt.Errorf("settings: scan feature gate key: %w", err)
		}
		out[key] = enabled
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("settings: iterate feature gate keys: %w", err)
	}
	return out, nil
}

// Invalidate drops cached entries for a tenant + key. Used by the LISTEN
// callback (see listener.go).
func (r *Resolver) Invalidate(tenantID uuid.UUID, key Key) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if per, ok := r.cache[tenantID]; ok {
		delete(per, key)
	}
}
