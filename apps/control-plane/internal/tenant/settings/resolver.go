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

// AllEnabled resolves every gate key registered in public.feature_gate_keys
// for tenantID in a single query, returning enabled=false for any key with
// no tenant_settings row. This backs the featuregate handler's dynamic
// response (issue #293): adding a new gate key is a migration-only change
// (INSERT INTO feature_gate_keys, plus ALTER TYPE ... ADD VALUE for a
// genuinely new tenant_setting_key) — this method never changes.
//
// Unlike IsEnabled, this bypasses the per-key in-memory cache: it always
// issues one fresh, indexed (tenant_id) query. The edge-api Gate already
// caches the whole response per tenant for 30s with singleflight dedup on
// cold misses, so a second cache layer here would add complexity without a
// measurable latency win at this call volume.
func (r *Resolver) AllEnabled(ctx context.Context, tenantID uuid.UUID) (map[Key]bool, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT k.key, COALESCE(s.enabled, false) AS enabled
		  FROM public.feature_gate_keys k
		  LEFT JOIN public.tenant_settings s
		    ON s.tenant_id = $1 AND s.key = k.key`,
		tenantID)
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

// ErrUnknownGateKey is returned by Set when key is not registered in
// public.feature_gate_keys. Callers should map it to a 400 rather than a 500:
// it means the caller asked to toggle a key the registry does not expose, not
// that anything failed.
var ErrUnknownGateKey = errors.New("settings: unknown feature gate key")

// GateKey is one row of the public.feature_gate_keys registry: the tenant
// setting key plus its human label and grouping category. It is the shape the
// admin feature-gate UI (issue #292) lists so a non-technical operator sees
// "Public billing" grouped under "billing" rather than the raw enum symbol.
type GateKey struct {
	Key      Key
	Label    string
	Category string
}

// Registry returns every gate key registered in public.feature_gate_keys,
// ordered by category then label so the admin UI groups them stably. Like
// AllEnabled it reads through the service-role pool (feature_gate_keys grants
// nothing to the authenticated role by design); the owner gate lives at the
// HTTP layer.
func (r *Resolver) Registry(ctx context.Context) ([]GateKey, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT key, label, category
		  FROM public.feature_gate_keys
		 ORDER BY category, label`)
	if err != nil {
		return nil, fmt.Errorf("settings: query feature gate registry: %w", err)
	}
	defer rows.Close()

	var out []GateKey
	for rows.Next() {
		var g GateKey
		if err := rows.Scan(&g.Key, &g.Label, &g.Category); err != nil {
			return nil, fmt.Errorf("settings: scan feature gate registry: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("settings: iterate feature gate registry: %w", err)
	}
	return out, nil
}

// Set upserts the enabled state of a single gate key for a tenant and records
// updatedBy (an auth.users id, or uuid.Nil to leave updated_by null). key must
// be registered in public.feature_gate_keys or ErrUnknownGateKey is returned;
// this both gives the admin UI a clean 400 and keeps the enum cast below from
// ever seeing an arbitrary string.
//
// The write fires the tenant_settings_changed NOTIFY trigger, so every
// control-plane and edge-api cache converges within its TTL. Set also
// invalidates the local per-key cache immediately so a read on this instance is
// fresh at once.
//
// This is the single sanctioned write path for tenant_settings; direct queries
// against the table from other packages are blocked by lint.
func (r *Resolver) Set(ctx context.Context, tenantID uuid.UUID, key Key, enabled bool, updatedBy uuid.UUID) error {
	var known bool
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM public.feature_gate_keys WHERE key::text = $1)`,
		string(key)).Scan(&known); err != nil {
		return fmt.Errorf("settings: check feature gate key: %w", err)
	}
	if !known {
		return ErrUnknownGateKey
	}

	var updatedByArg any
	if updatedBy != uuid.Nil {
		updatedByArg = updatedBy
	}

	if _, err := r.pool.Exec(ctx, `
		INSERT INTO public.tenant_settings (tenant_id, key, enabled, updated_by, updated_at)
		VALUES ($1, $2::public.tenant_setting_key, $3, $4, now())
		ON CONFLICT (tenant_id, key)
		DO UPDATE SET enabled = EXCLUDED.enabled,
		              updated_by = EXCLUDED.updated_by,
		              updated_at = now()`,
		tenantID, string(key), enabled, updatedByArg); err != nil {
		return fmt.Errorf("settings: upsert tenant setting: %w", err)
	}

	r.Invalidate(tenantID, key)
	return nil
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
