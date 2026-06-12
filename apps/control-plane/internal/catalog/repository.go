package catalog

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	ListPublicAliases(ctx context.Context) ([]ModelAlias, error)
	// ListAliasesForTenant returns model aliases visible to the given tenant,
	// applying the visibility rules: public by default, restricted by grant,
	// explicitly blocked when visible=false row exists.
	ListAliasesForTenant(ctx context.Context, tenantID uuid.UUID) ([]ModelAlias, error)
	GetSnapshot(ctx context.Context) (CatalogSnapshot, error)
	// GetAlias fetches a single model alias by its alias_id.
	// Used by syncOWUI to determine the alias visibility class before choosing
	// the OWUI sync strategy (restricted vs public/preview).
	GetAlias(ctx context.Context, aliasID string) (ModelAlias, error)
	// Visibility admin operations for tenant_model_visibility table.
	GetVisibilityRows(ctx context.Context, tenantID uuid.UUID) ([]TenantModelVisibility, error)
	UpsertVisibility(ctx context.Context, row TenantModelVisibility) error
	DeleteVisibility(ctx context.Context, tenantID uuid.UUID, aliasID string) error
	GetAllVisibleTenantsForAlias(ctx context.Context, aliasID string) ([]TenantModelVisibility, error)
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

// ListAliasesForTenant implements the tenant visibility filtering rules using a
// LEFT JOIN against tenant_model_visibility. Rules (in priority order):
//  1. If a tenant_model_visibility row with visible=false exists, exclude.
//  2. If visibility='restricted' and no visible=true row exists, exclude.
//  3. Otherwise include (covers public, preview with no override).
func (r *pgxRepository) ListAliasesForTenant(ctx context.Context, tenantID uuid.UUID) ([]ModelAlias, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			a.alias_id,
			a.owned_by,
			a.display_name,
			a.summary,
			a.visibility,
			a.lifecycle,
			a.capability_badges,
			a.input_price_credits,
			a.output_price_credits,
			a.cache_read_price_credits,
			a.cache_write_price_credits,
			a.created_at,
			a.updated_at
		FROM public.model_aliases a
		LEFT JOIN public.tenant_model_visibility v
			ON v.alias_id = a.alias_id AND v.tenant_id = $1
		WHERE
			-- Explicitly blocked rows are always excluded.
			(v.visible IS NULL OR v.visible = true)
			AND (
				-- Public / preview: visible by default.
				a.visibility IN ('public', 'preview')
				-- Restricted: only if a visible=true row exists.
				OR (a.visibility = 'restricted' AND v.visible = true)
			)
		ORDER BY a.alias_id ASC
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("catalog: list aliases for tenant: %w", err)
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
		return nil, fmt.Errorf("catalog: iterate tenant aliases: %w", err)
	}
	return aliases, nil
}

// GetAlias returns a single model alias by its alias_id.
func (r *pgxRepository) GetAlias(ctx context.Context, aliasID string) (ModelAlias, error) {
	row := r.pool.QueryRow(ctx, `
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
		WHERE alias_id = $1
	`, aliasID)

	alias, err := scanModelAlias(row)
	if err != nil {
		return ModelAlias{}, fmt.Errorf("catalog: get alias %q: %w", aliasID, err)
	}
	return alias, nil
}

// GetVisibilityRows returns all tenant_model_visibility rows for a given tenant.
func (r *pgxRepository) GetVisibilityRows(ctx context.Context, tenantID uuid.UUID) ([]TenantModelVisibility, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT tenant_id, alias_id, visible, updated_at
		FROM public.tenant_model_visibility
		WHERE tenant_id = $1
		ORDER BY alias_id ASC
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("catalog: get visibility rows: %w", err)
	}
	defer rows.Close()

	var out []TenantModelVisibility
	for rows.Next() {
		var row TenantModelVisibility
		if err := rows.Scan(&row.TenantID, &row.AliasID, &row.Visible, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("catalog: scan visibility row: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate visibility rows: %w", err)
	}
	return out, nil
}

// UpsertVisibility inserts or updates a tenant_model_visibility row.
func (r *pgxRepository) UpsertVisibility(ctx context.Context, row TenantModelVisibility) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO public.tenant_model_visibility (tenant_id, alias_id, visible, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (tenant_id, alias_id) DO UPDATE
			SET visible = EXCLUDED.visible, updated_at = now()
	`, row.TenantID, row.AliasID, row.Visible)
	if err != nil {
		return fmt.Errorf("catalog: upsert visibility: %w", err)
	}
	return nil
}

// DeleteVisibility sets visible=false for the (tenantID, aliasID) pair.
// It uses a soft delete (visible=false) rather than a physical row delete
// so that explicit blocks are preserved and distinguishable from the absence
// of a row (the "no override" state).
func (r *pgxRepository) DeleteVisibility(ctx context.Context, tenantID uuid.UUID, aliasID string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO public.tenant_model_visibility (tenant_id, alias_id, visible, updated_at)
		VALUES ($1, $2, false, now())
		ON CONFLICT (tenant_id, alias_id) DO UPDATE
			SET visible = false, updated_at = now()
	`, tenantID, aliasID)
	if err != nil {
		return fmt.Errorf("catalog: delete visibility: %w", err)
	}
	return nil
}

// GetAllVisibleTenantsForAlias returns all tenant_model_visibility rows for an
// alias where visible=true. Used by the visibility admin endpoint to build the
// full OWUI allowlist after any PUT/DELETE operation.
func (r *pgxRepository) GetAllVisibleTenantsForAlias(ctx context.Context, aliasID string) ([]TenantModelVisibility, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT tenant_id, alias_id, visible, updated_at
		FROM public.tenant_model_visibility
		WHERE alias_id = $1 AND visible = true
		ORDER BY tenant_id ASC
	`, aliasID)
	if err != nil {
		return nil, fmt.Errorf("catalog: get visible tenants for alias: %w", err)
	}
	defer rows.Close()

	var out []TenantModelVisibility
	for rows.Next() {
		var row TenantModelVisibility
		if err := rows.Scan(&row.TenantID, &row.AliasID, &row.Visible, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("catalog: scan visible tenant row: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: iterate visible tenant rows: %w", err)
	}
	return out, nil
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
