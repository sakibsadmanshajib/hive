package providers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a provider row does not exist.
var ErrNotFound = errors.New("provider not found")

// ErrSlugConflict is returned when a create or update violates the slug unique constraint.
var ErrSlugConflict = errors.New("provider slug already exists")

// Provider represents a row in public.custom_providers.
type Provider struct {
	ID            uuid.UUID `json:"id"             db:"id"`
	Slug          string    `json:"slug"           db:"slug"`
	DisplayName   string    `json:"display_name"   db:"display_name"`
	BaseURL       string    `json:"base_url"       db:"base_url"`
	APIKeyEnv     string    `json:"api_key_env"    db:"api_key_env"`
	LiteLLMPrefix string    `json:"litellm_prefix" db:"litellm_prefix"`
	Enabled       bool      `json:"enabled"        db:"enabled"`
	CreatedAt     time.Time `json:"created_at"     db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"     db:"updated_at"`
}

// Repository defines the data-access contract for custom_providers.
type Repository interface {
	Create(ctx context.Context, p Provider) (Provider, error)
	List(ctx context.Context) ([]Provider, error)
	Get(ctx context.Context, id uuid.UUID) (Provider, error)
	Update(ctx context.Context, id uuid.UUID, p Provider) (Provider, error)
	Delete(ctx context.Context, id uuid.UUID) error // soft: sets enabled=false
}

type pgxRepository struct {
	pool *pgxpool.Pool
}

// NewPgxRepository returns a Repository backed by the given connection pool.
func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

func (r *pgxRepository) Create(ctx context.Context, p Provider) (Provider, error) {
	p.ID = uuid.New()
	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now

	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.custom_providers
			(id, slug, display_name, base_url, api_key_env, litellm_prefix, enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, slug, display_name, base_url, api_key_env, litellm_prefix, enabled, created_at, updated_at`,
		p.ID, p.Slug, p.DisplayName, p.BaseURL, p.APIKeyEnv, p.LiteLLMPrefix, p.Enabled, p.CreatedAt, p.UpdatedAt,
	)

	var out Provider
	if err := row.Scan(
		&out.ID, &out.Slug, &out.DisplayName, &out.BaseURL,
		&out.APIKeyEnv, &out.LiteLLMPrefix, &out.Enabled,
		&out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		if isUniqueViolation(err) {
			return Provider{}, ErrSlugConflict
		}
		return Provider{}, fmt.Errorf("providers: create: %w", err)
	}
	return out, nil
}

func (r *pgxRepository) List(ctx context.Context) ([]Provider, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, slug, display_name, base_url, api_key_env, litellm_prefix, enabled, created_at, updated_at
		FROM public.custom_providers
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("providers: list: %w", err)
	}
	defer rows.Close()

	var out []Provider
	for rows.Next() {
		var p Provider
		if err := rows.Scan(
			&p.ID, &p.Slug, &p.DisplayName, &p.BaseURL,
			&p.APIKeyEnv, &p.LiteLLMPrefix, &p.Enabled,
			&p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("providers: list scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("providers: list rows: %w", err)
	}
	if out == nil {
		out = []Provider{}
	}
	return out, nil
}

func (r *pgxRepository) Get(ctx context.Context, id uuid.UUID) (Provider, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, slug, display_name, base_url, api_key_env, litellm_prefix, enabled, created_at, updated_at
		FROM public.custom_providers
		WHERE id = $1`, id)

	var p Provider
	if err := row.Scan(
		&p.ID, &p.Slug, &p.DisplayName, &p.BaseURL,
		&p.APIKeyEnv, &p.LiteLLMPrefix, &p.Enabled,
		&p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Provider{}, ErrNotFound
		}
		return Provider{}, fmt.Errorf("providers: get: %w", err)
	}
	return p, nil
}

func (r *pgxRepository) Update(ctx context.Context, id uuid.UUID, p Provider) (Provider, error) {
	now := time.Now().UTC()
	row := r.pool.QueryRow(ctx, `
		UPDATE public.custom_providers
		SET slug=$2, display_name=$3, base_url=$4, api_key_env=$5, litellm_prefix=$6, enabled=$7, updated_at=$8
		WHERE id=$1
		RETURNING id, slug, display_name, base_url, api_key_env, litellm_prefix, enabled, created_at, updated_at`,
		id, p.Slug, p.DisplayName, p.BaseURL, p.APIKeyEnv, p.LiteLLMPrefix, p.Enabled, now,
	)

	var out Provider
	if err := row.Scan(
		&out.ID, &out.Slug, &out.DisplayName, &out.BaseURL,
		&out.APIKeyEnv, &out.LiteLLMPrefix, &out.Enabled,
		&out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Provider{}, ErrNotFound
		}
		if isUniqueViolation(err) {
			return Provider{}, ErrSlugConflict
		}
		return Provider{}, fmt.Errorf("providers: update: %w", err)
	}
	return out, nil
}

func (r *pgxRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE public.custom_providers SET enabled=false, updated_at=$2 WHERE id=$1`,
		id, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("providers: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// isUniqueViolation returns true when err is a Postgres unique-constraint
// violation (SQLSTATE 23505). Uses pgconn.PgError type assertion so the check
// is exact and cannot false-positive on wrapped error messages.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
