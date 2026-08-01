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
	// LoadAliasPricing reads the alias's per-model credit price from
	// public.model_aliases. A missing row is reported as the
	// catalog.CatalogPricing zero value rather than an error, because the
	// caller cannot distinguish "no row" from "row priced at zero" and both
	// mean the same thing: no cost basis. SelectRoute turns that zero value
	// into ErrAliasNotPriced and refuses the selection (issue #617), so a
	// missing price fails closed there, not here.
	LoadAliasPricing(ctx context.Context, aliasID string) (catalog.CatalogPricing, error)
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

// LoadAliasPricing reads public.model_aliases directly rather than going
// through catalog.Service: routing already depends on the catalog package
// for AliasPolicySnapshot, and a same-transactionable direct read keeps this
// addition to one query on the pool routing already holds, with no new
// cross-package call and no new network hop (metering step 2 brief section
// 2.5).
func (r *pgxRepository) LoadAliasPricing(ctx context.Context, aliasID string) (catalog.CatalogPricing, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			input_price_credits,
			output_price_credits,
			cache_read_price_credits,
			cache_write_price_credits
		FROM public.model_aliases
		WHERE alias_id = $1
	`, aliasID)

	var pricing catalog.CatalogPricing
	if err := row.Scan(
		&pricing.InputPriceCredits,
		&pricing.OutputPriceCredits,
		&pricing.CacheReadPriceCredits,
		&pricing.CacheWritePriceCredits,
	); err != nil {
		if err == pgx.ErrNoRows {
			// No model_aliases row for an alias the routing tables already
			// resolved is a data-inconsistency case, not an infrastructure
			// failure, so it is reported as "no cost basis" (the zero value)
			// rather than a query error. SelectRoute is what refuses on it;
			// see ErrAliasNotPriced. A provider_routes row cannot outlive its
			// alias anyway (FK with ON DELETE CASCADE), so in practice this
			// branch is only reachable if the two reads race a deletion.
			return catalog.CatalogPricing{}, nil
		}
		return catalog.CatalogPricing{}, fmt.Errorf("routing: load alias pricing: %w", err)
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
