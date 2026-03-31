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

	return buildCatalogSnapshot(aliases), nil
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
