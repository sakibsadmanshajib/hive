package routing

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	LoadAliasPolicy(ctx context.Context, aliasID string) (catalog.AliasPolicySnapshot, error)
	ListRouteCandidates(ctx context.Context, aliasID string) ([]RouteCandidate, error)
	// LoadRoutePricing reads the SELECTED route's per-route credit price
	// from public.provider_routes (D-032: price lives on the route, not the
	// alias, because one alias can carry routes to providers at very
	// different real cost -- see SelectionResult.Pricing). A missing row IS
	// an error: fail closed, the same posture as the tenant entitlement
	// check above. provider_routes.input_price_credits/output_price_credits
	// are NOT NULL for every route seeded today, so this is only reachable
	// via a genuine data or infrastructure fault, never a silent free ride.
	// Cache read/write pricing is not carried per-route: precedence.go's
	// billing formula never reads it, so CacheReadPriceCredits/
	// CacheWritePriceCredits are always nil on the returned value.
	LoadRoutePricing(ctx context.Context, routeID string) (catalog.CatalogPricing, error)
}

type pgxRepository struct {
	pool *pgxpool.Pool
}

func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

func (r *pgxRepository) LoadAliasPolicy(ctx context.Context, aliasID string) (catalog.AliasPolicySnapshot, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			alias_id,
			policy_mode,
			allow_price_class_widening,
			fallback_order
		FROM public.alias_route_policies
		WHERE alias_id = $1
	`, aliasID)

	var policy catalog.AliasPolicySnapshot
	var fallbackOrder []byte
	if err := row.Scan(
		&policy.AliasID,
		&policy.PolicyMode,
		&policy.AllowPriceClassWidening,
		&fallbackOrder,
	); err != nil {
		if err == pgx.ErrNoRows {
			return catalog.AliasPolicySnapshot{}, fmt.Errorf("%w: %s", ErrAliasNotFound, aliasID)
		}
		return catalog.AliasPolicySnapshot{}, fmt.Errorf("routing: load alias policy: %w", err)
	}

	policy.FallbackOrder = []string{}
	if len(fallbackOrder) > 0 {
		if err := json.Unmarshal(fallbackOrder, &policy.FallbackOrder); err != nil {
			return catalog.AliasPolicySnapshot{}, fmt.Errorf("routing: decode fallback order: %w", err)
		}
	}

	return policy, nil
}

func (r *pgxRepository) ListRouteCandidates(ctx context.Context, aliasID string) ([]RouteCandidate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			r.route_id,
			r.alias_id,
			r.provider,
			r.provider_model,
			r.litellm_model_name,
			r.price_class,
			r.health_state,
			r.priority,
			c.supports_responses,
			c.supports_chat_completions,
			c.supports_completions,
			c.supports_embeddings,
			c.supports_streaming,
			c.supports_reasoning,
			c.supports_cache_read,
			c.supports_cache_write,
			c.supports_image_generation,
			c.supports_image_edit,
			c.supports_tts,
			c.supports_stt,
			c.supports_batch,
			c.tools_supported
		FROM public.provider_routes r
		JOIN public.provider_capabilities c ON c.route_id = r.route_id
		WHERE r.alias_id = $1
		ORDER BY r.priority ASC, r.route_id ASC
	`, aliasID)
	if err != nil {
		return nil, fmt.Errorf("routing: list route candidates: %w", err)
	}
	defer rows.Close()

	var candidates []RouteCandidate
	for rows.Next() {
		candidate, err := scanRouteCandidate(rows)
		if err != nil {
			return nil, err
		}

		candidates = append(candidates, candidate)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("routing: iterate route candidates: %w", err)
	}

	return candidates, nil
}

// LoadRoutePricing reads public.provider_routes directly rather than going
// through catalog.Service: routing already depends on the catalog package
// for AliasPolicySnapshot, and a same-transactionable direct read keeps this
// addition to one query on the pool routing already holds, with no new
// cross-package call and no new network hop (D-032, following the same
// convention the alias-keyed version used).
//
// WHERE pricing_unit = 'tokens' is D-032's pricing-unit contract: this
// routing package only ever meters tokens (chat completions), so a route
// whose pricing_unit is anything else must be rejected rather than
// mispriced. That row simply does not match this query, so it falls through
// the existing pgx.ErrNoRows fail-closed branch below with no separate
// mismatch check needed.
func (r *pgxRepository) LoadRoutePricing(ctx context.Context, routeID string) (catalog.CatalogPricing, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			input_price_credits,
			output_price_credits
		FROM public.provider_routes
		WHERE route_id = $1 AND pricing_unit = 'tokens'
	`, routeID)

	var pricing catalog.CatalogPricing
	if err := row.Scan(
		&pricing.InputPriceCredits,
		&pricing.OutputPriceCredits,
	); err != nil {
		// Fail closed (D-032): unlike the old alias-keyed lookup, a missing
		// or unreadable price row is a real error, not a zero-value success.
		// A route the routing tables already resolved but that carries no
		// price is a data-inconsistency serious enough to refuse the whole
		// selection rather than silently bill it as free.
		return catalog.CatalogPricing{}, fmt.Errorf("routing: load route pricing for %s: %w", routeID, err)
	}

	return pricing, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRouteCandidate(scanner rowScanner) (RouteCandidate, error) {
	var candidate RouteCandidate
	if err := scanner.Scan(
		&candidate.RouteID,
		&candidate.AliasID,
		&candidate.Provider,
		&candidate.ProviderModel,
		&candidate.LiteLLMModelName,
		&candidate.PriceClass,
		&candidate.HealthState,
		&candidate.Priority,
		&candidate.SupportsResponses,
		&candidate.SupportsChatCompletions,
		&candidate.SupportsCompletions,
		&candidate.SupportsEmbeddings,
		&candidate.SupportsStreaming,
		&candidate.SupportsReasoning,
		&candidate.SupportsCacheRead,
		&candidate.SupportsCacheWrite,
		&candidate.SupportsImageGeneration,
		&candidate.SupportsImageEdit,
		&candidate.SupportsTTS,
		&candidate.SupportsSTT,
		&candidate.SupportsBatch,
		&candidate.SupportsTools,
	); err != nil {
		if err == pgx.ErrNoRows {
			return RouteCandidate{}, fmt.Errorf("routing: route candidate not found")
		}
		return RouteCandidate{}, fmt.Errorf("routing: scan route candidate: %w", err)
	}

	return candidate, nil
}
