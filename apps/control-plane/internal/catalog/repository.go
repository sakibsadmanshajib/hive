package catalog

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	ListPublicAliases(ctx context.Context) ([]ModelAlias, error)
	GetSnapshot(ctx context.Context) (CatalogSnapshot, error)
}

type pgxRepository struct {
	pool *pgxpool.Pool
}

func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

func (r *pgxRepository) ListPublicAliases(ctx context.Context) ([]ModelAlias, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			alias_id,
			owned_by,
			display_name,
			summary,
			visibility,
			lifecycle,
			capability_badges,
			input_price_credits,
			output_price_credits,
			cache_read_price_credits,
			cache_write_price_credits,
			created_at,
			updated_at
		FROM public.model_aliases
		WHERE visibility IN ('public', 'preview')
		ORDER BY alias_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("catalog: list public aliases: %w", err)
	}
	defer rows.Close()

	var aliases []ModelAlias
	for rows.Next() {
		alias, err := scanModelAlias(rows)
		if err != nil {
			return nil, err
		}

		aliases = append(aliases, alias)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate aliases: %w", err)
	}

	return aliases, nil
}

func (r *pgxRepository) GetSnapshot(ctx context.Context) (CatalogSnapshot, error) {
	aliases, err := r.ListPublicAliases(ctx)
	if err != nil {
		return CatalogSnapshot{}, err
	}

	routes, err := r.listRouteSnapshots(ctx)
	if err != nil {
		return CatalogSnapshot{}, err
	}

	policies, err := r.listAliasPolicies(ctx)
	if err != nil {
		return CatalogSnapshot{}, err
	}

	snapshot := buildCatalogSnapshot(aliases)
	snapshot.Routes = routes
	snapshot.AliasPolicies = policies

	return snapshot, nil
}

type aliasScanner interface {
	Scan(dest ...any) error
}

func scanModelAlias(scanner aliasScanner) (ModelAlias, error) {
	var alias ModelAlias
	var capabilityBadges []byte

	if err := scanner.Scan(
		&alias.AliasID,
		&alias.OwnedBy,
		&alias.DisplayName,
		&alias.Summary,
		&alias.Visibility,
		&alias.Lifecycle,
		&capabilityBadges,
		&alias.InputPriceCredits,
		&alias.OutputPriceCredits,
		&alias.CacheReadPriceCredits,
		&alias.CacheWritePriceCredits,
		&alias.CreatedAt,
		&alias.UpdatedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return ModelAlias{}, fmt.Errorf("catalog: model alias not found")
		}
		return ModelAlias{}, fmt.Errorf("catalog: scan model alias: %w", err)
	}

	alias.CapabilityBadges = []string{}
	if len(capabilityBadges) > 0 {
		if err := json.Unmarshal(capabilityBadges, &alias.CapabilityBadges); err != nil {
			return ModelAlias{}, fmt.Errorf("catalog: decode capability badges: %w", err)
		}
	}

	return alias, nil
}

func (r *pgxRepository) listRouteSnapshots(ctx context.Context) ([]RouteSnapshot, error) {
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
			c.supports_cache_write
		FROM public.provider_routes r
		JOIN public.provider_capabilities c ON c.route_id = r.route_id
		ORDER BY r.alias_id ASC, r.priority ASC, r.route_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("catalog: list route snapshots: %w", err)
	}
	defer rows.Close()

	var routes []RouteSnapshot
	for rows.Next() {
		route, err := scanRouteSnapshot(rows)
		if err != nil {
			return nil, err
		}

		routes = append(routes, route)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate route snapshots: %w", err)
	}

	return routes, nil
}

func (r *pgxRepository) listAliasPolicies(ctx context.Context) ([]AliasPolicySnapshot, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			alias_id,
			policy_mode,
			allow_price_class_widening,
			fallback_order
		FROM public.alias_route_policies
		ORDER BY alias_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("catalog: list alias policies: %w", err)
	}
	defer rows.Close()

	var policies []AliasPolicySnapshot
	for rows.Next() {
		policy, err := scanAliasPolicy(rows)
		if err != nil {
			return nil, err
		}

		policies = append(policies, policy)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate alias policies: %w", err)
	}

	return policies, nil
}

func scanRouteSnapshot(scanner aliasScanner) (RouteSnapshot, error) {
	var route RouteSnapshot
	if err := scanner.Scan(
		&route.RouteID,
		&route.AliasID,
		&route.Provider,
		&route.ProviderModel,
		&route.LiteLLMModelName,
		&route.PriceClass,
		&route.HealthState,
		&route.Priority,
		&route.SupportsResponses,
		&route.SupportsChatCompletions,
		&route.SupportsCompletions,
		&route.SupportsEmbeddings,
		&route.SupportsStreaming,
		&route.SupportsReasoning,
		&route.SupportsCacheRead,
		&route.SupportsCacheWrite,
	); err != nil {
		if err == pgx.ErrNoRows {
			return RouteSnapshot{}, fmt.Errorf("catalog: route snapshot not found")
		}
		return RouteSnapshot{}, fmt.Errorf("catalog: scan route snapshot: %w", err)
	}

	return route, nil
}

func scanAliasPolicy(scanner aliasScanner) (AliasPolicySnapshot, error) {
	var policy AliasPolicySnapshot
	var fallbackOrder []byte

	if err := scanner.Scan(
		&policy.AliasID,
		&policy.PolicyMode,
		&policy.AllowPriceClassWidening,
		&fallbackOrder,
	); err != nil {
		if err == pgx.ErrNoRows {
			return AliasPolicySnapshot{}, fmt.Errorf("catalog: alias policy not found")
		}
		return AliasPolicySnapshot{}, fmt.Errorf("catalog: scan alias policy: %w", err)
	}

	policy.FallbackOrder = []string{}
	if len(fallbackOrder) > 0 {
		if err := json.Unmarshal(fallbackOrder, &policy.FallbackOrder); err != nil {
			return AliasPolicySnapshot{}, fmt.Errorf("catalog: decode fallback order: %w", err)
		}
	}

	return policy, nil
}
