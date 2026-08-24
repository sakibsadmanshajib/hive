package routing

import (
	"context"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/rcache"
)

// CachedRepository wraps a Repository with a short-TTL read-through Redis
// cache over the three reads SelectRoute makes on every metered request:
// LoadAliasPolicy, ListRouteCandidates, and LoadAliasPricing.
//
// These rows are global catalog reference data (no tenant input reaches these
// queries, so no cache key can mix tenants). They have NO Go-side writers:
// provider_routes, alias_route_policies and model_aliases rows are written by
// SQL migrations only (providers/repository.go records that routes are
// migration-only), so explicit invalidation has no runtime hook to attach to;
// the 30s TTL is the staleness bound for migration-time changes.
//
// Money-path boundary: LoadAliasPricing caches the published per-million
// credit price of an alias, not any account's balance, hold or ledger. The
// charge itself is computed from the pricing row captured at select time and
// settled transactionally in the accounting path, which this package never
// touches. Prices change via migration; worst case a new price applies to
// requests starting up to DefaultTTL after the migration lands.
type CachedRepository struct {
	Repository
	cache *rcache.Cache
}

// NewCachedRepository returns repo wrapped in the Redis read-through cache.
// A nil cache returns repo unchanged.
func NewCachedRepository(repo Repository, cache *rcache.Cache) Repository {
	if cache == nil {
		return repo
	}
	return &CachedRepository{Repository: repo, cache: cache}
}

// keyPolicy, keyCandidates and keyPrice name the three cache keys. Alias IDs
// are the natural key of every cached row set; they are lowercase slug-style
// strings from public.model_aliases.alias_id.
func keyPolicy(k string) string { return "rt:pol:" + k }

func keyCandidates(k string) string { return "rt:cand:" + k }

func keyPrice(k string) string { return "rt:price:" + k }

// LoadAliasPolicy caches a successful policy read. Errors are never cached:
// an ErrAliasNotFound answer re-queries the database until the alias exists,
// so an alias created by migration becomes routable immediately rather than
// after a TTL.
func (r *CachedRepository) LoadAliasPolicy(ctx context.Context, aliasID string) (catalog.AliasPolicySnapshot, error) {
	return rcache.GetJSON(ctx, r.cache, keyPolicy(aliasID), func(ctx context.Context) (catalog.AliasPolicySnapshot, error) {
		return r.Repository.LoadAliasPolicy(ctx, aliasID)
	})
}

// ListRouteCandidates caches the route candidate list for one alias. A nil or
// empty result is cached like any other successful read so an alias with no
// routes does not hammer the database either; JSON null round-trips back to
// nil, preserving the zero-value semantics ListRouteCandidates documents.
func (r *CachedRepository) ListRouteCandidates(ctx context.Context, aliasID string) ([]RouteCandidate, error) {
	var loaded []RouteCandidate
	got, err := rcache.GetJSON(ctx, r.cache, keyCandidates(aliasID), func(ctx context.Context) ([]RouteCandidate, error) {
		rows, err := r.Repository.ListRouteCandidates(ctx, aliasID)
		if err != nil {
			return nil, err
		}
		if rows == nil {
			rows = []RouteCandidate{}
		}
		return rows, nil
	})
	if err != nil {
		return nil, err
	}
	loaded = got
	return loaded, nil
}

// LoadAliasPricing caches the published per-million credit price. See the
// money-path boundary note on CachedRepository above: balances, reservations
// and ledger entries are never cached anywhere in this change.
// LoadAliasPricing caches the published per-million credit price. See the
// money-path boundary note on CachedRepository above: balances, reservations
// and ledger entries are never cached anywhere in this change.
func (r *CachedRepository) LoadAliasPricing(ctx context.Context, aliasID string) (catalog.CatalogPricing, string, error) {
	type pricingRow struct {
		Pricing catalog.CatalogPricing `json:"pricing"`
		Unit    string                 `json:"unit"`
	}
	row, err := rcache.GetJSON(ctx, r.cache, keyPrice(aliasID), func(ctx context.Context) (pricingRow, error) {
		p, unit, err := r.Repository.LoadAliasPricing(ctx, aliasID)
		if err != nil {
			return pricingRow{}, err
		}
		return pricingRow{Pricing: p, Unit: unit}, nil
	})
	if err != nil {
		return catalog.CatalogPricing{}, "", err
	}
	return row.Pricing, row.Unit, nil
}
