package catalog

import (
	"context"
	"log"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/rcache"
)

// CachedRepository wraps a catalog Repository with a short-TTL read-through
// Redis cache over the hot reads the /v1/models listing and the inference
// entitlement check make per request.
//
// TENANT SCOPING (cache-poisoning guard): every key that carries
// tenant-derived data embeds the tenant UUID, so two tenants can never read
// each other's visibility verdicts or model lists through the shared Redis.
// Only ListPublicAliases (the unauthenticated public/preview alias list) is
// keyed without a tenant, because its query takes no tenant input and its
// rows are identical for every caller.
//
// Invalidation is write-through at the mutation points this package owns:
// UpsertVisibility and DeleteVisibility delete exactly the keys derived from
// their (tenant, alias) arguments. ReconcileOWUISync and the admin visibility
// handlers both mutate through these same repository methods, so both paths
// invalidate automatically. The 30s TTL backstops anything missed.
type CachedRepository struct {
	Repository
	cache *rcache.Cache
}

// NewCachedRepository returns repo wrapped with the Redis read-through cache,
// or repo unchanged when cache is nil.
func NewCachedRepository(repo Repository, cache *rcache.Cache) Repository {
	if cache == nil {
		return repo
	}
	return &CachedRepository{Repository: repo, cache: cache}
}

func keyPublic() string { return "cat:public" }

func keyTenantList(tid uuid.UUID) string { return "cat:tlist:" + tid.String() }

func keyToolCaps() string { return "cat:toolcaps" }

func keyVisible(tid uuid.UUID, aliasID string) string {
	return "cat:vis:" + tid.String() + ":" + aliasID
}

// ListPublicAliases caches the global public/preview alias list. Keyed without
// a tenant on purpose: the underlying query takes no tenant input. Nil results
// marshal as JSON null and unmarshal back to nil, preserving raw semantics.
func (r *CachedRepository) ListPublicAliases(ctx context.Context) ([]ModelAlias, error) {
	return rcache.GetJSON(ctx, r.cache, keyPublic(), r.Repository.ListPublicAliases)
}

// ListRouteToolCapabilities caches the route-to-capability rows behind
// hive_capabilities.tools. Keyed without a tenant for the same reason
// ListPublicAliases is: the query takes no tenant input and its rows are
// identical for every caller. It sits on GET /v1/models, which the chat model
// picker hits on every page load, so it gets the same 30s read-through the rest
// of that endpoint already has rather than being the one uncached query on it.
//
// Nothing in this package writes provider_capabilities (migrations and the
// route admin surfaces do), so there is no write-through invalidation point to
// add. The TTL is the whole staleness bound, and it is the same bound the alias
// list next to it already accepts.
func (r *CachedRepository) ListRouteToolCapabilities(ctx context.Context) ([]RouteToolCapability, error) {
	return rcache.GetJSON(ctx, r.cache, keyToolCaps(), r.Repository.ListRouteToolCapabilities)
}

// ListAliasesForTenant caches one tenant's entitled alias list under a key
// that embeds the tenant UUID (see the tenant-scoping note above).
func (r *CachedRepository) ListAliasesForTenant(ctx context.Context, tenantID uuid.UUID) ([]ModelAlias, error) {
	return rcache.GetJSON(ctx, r.cache, keyTenantList(tenantID), func(ctx context.Context) ([]ModelAlias, error) {
		return r.Repository.ListAliasesForTenant(ctx, tenantID)
	})
}

// IsAliasVisibleToTenant caches one tenant's entitlement verdict for one
// alias, keyed by both. A false verdict IS cached: it is a real answer, not
// an error, and the mutation paths below invalidate it.
func (r *CachedRepository) IsAliasVisibleToTenant(ctx context.Context, tenantID uuid.UUID, aliasID string) (bool, error) {
	return rcache.GetJSON(ctx, r.cache, keyVisible(tenantID, aliasID), func(ctx context.Context) (bool, error) {
		return r.Repository.IsAliasVisibleToTenant(ctx, tenantID, aliasID)
	})
}

// UpsertVisibility delegates the write, then deletes exactly the cache keys
// derived from this (tenant, alias): the tenant's model list and its single
// alias entitlement verdict.
//
// A failed invalidation does NOT fail the method: the visibility row has
// already committed, so the caller must see success. It is logged loudly
// instead. The residual risk is bounded: a stale cached verdict can serve
// for at most one TTL (30s), the same bound every other staleness window in
// this cache has.
func (r *CachedRepository) UpsertVisibility(ctx context.Context, row TenantModelVisibility) error {
	if err := r.Repository.UpsertVisibility(ctx, row); err != nil {
		return err
	}
	if err := r.cache.Delete(ctx, keyTenantList(row.TenantID), keyVisible(row.TenantID, row.AliasID)); err != nil {
		log.Printf("WARNING: catalog cache: invalidation after UpsertVisibility(tenant=%s alias=%s) failed: %v; a stale entry may serve up to the cache TTL", row.TenantID, row.AliasID, err)
	}
	return nil
}

// DeleteVisibility mirrors UpsertVisibility's invalidation for the delete path.
func (r *CachedRepository) DeleteVisibility(ctx context.Context, tenantID uuid.UUID, aliasID string) error {
	if err := r.Repository.DeleteVisibility(ctx, tenantID, aliasID); err != nil {
		return err
	}
	if err := r.cache.Delete(ctx, keyTenantList(tenantID), keyVisible(tenantID, aliasID)); err != nil {
		log.Printf("WARNING: catalog cache: invalidation after DeleteVisibility(tenant=%s alias=%s) failed: %v; a stale entry may serve up to the cache TTL", tenantID, aliasID, err)
	}
	return nil
}
