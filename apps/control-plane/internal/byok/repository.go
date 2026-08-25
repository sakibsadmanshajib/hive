package byok

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinel errors surfaced by the repository.
var (
	ErrNotFound = errors.New("byok: key not found")
	// ErrConflict reports a duplicate active provider_slug for the same account.
	ErrConflict = errors.New("byok: active provider key already registered")
)

// Repository is the data-access contract for tenant_provider_keys. Every
// account-scoped method filters by account_id in the WHERE clause itself, so
// a cross-account read is indistinguishable from a missing row.
type Repository interface {
	Create(ctx context.Context, k Key) (Key, error)
	ListByAccount(ctx context.Context, accountID uuid.UUID) ([]Key, error)
	ListAll(ctx context.Context) ([]Key, error)
	Get(ctx context.Context, accountID, id uuid.UUID) (Key, error)
	// Revoke flips an active row to revoked. Timestamps are stamped by the
	// database clock (now()) so created_at and revoked_at can never invert
	// under app/server skew.
	Revoke(ctx context.Context, accountID, id uuid.UUID) (Key, error)
}

// timeNow is a test seam over the wall clock.
var timeNow = func() time.Time { return time.Now().UTC() }

type pgxRepository struct {
	pool *pgxpool.Pool
}

// NewPgxRepository returns a Repository backed by the given connection pool.
func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

const selectCols = `id, account_id, label, provider_slug, base_url, model_map,
	encrypted_api_key, key_last4, status, created_by_user_id, created_at,
	updated_at, revoked_at`

type scanTarget struct {
	key Key
}

func (s *scanTarget) columns() []any {
	return []any{&s.key.ID, &s.key.AccountID, &s.key.Label, &s.key.ProviderSlug,
		&s.key.BaseURL, &s.key.ModelMap, &s.key.EncryptedAPIKey, &s.key.KeyLast4,
		&s.key.Status, &s.key.CreatedBy, &s.key.CreatedAt, &s.key.UpdatedAt,
		&s.key.RevokedAt}
}

func (r *pgxRepository) Create(ctx context.Context, k Key) (Key, error) {
	sql := `insert into public.tenant_provider_keys
		(account_id, label, provider_slug, base_url, model_map,
		 encrypted_api_key, key_last4, status, created_by_user_id)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		returning ` + selectCols

	s := &scanTarget{}
	err := r.pool.QueryRow(ctx, sql,
		k.AccountID, k.Label, k.ProviderSlug, k.BaseURL, k.ModelMap,
		k.EncryptedAPIKey, k.KeyLast4, StatusActive, k.CreatedBy,
	).Scan(s.columns()...)
	if isUniqueViolation(err) {
		return Key{}, ErrConflict
	}
	if err != nil {
		return Key{}, err
	}
	return s.key, nil
}

func (r *pgxRepository) ListByAccount(ctx context.Context, accountID uuid.UUID) ([]Key, error) {
	sql := `select ` + selectCols + ` from public.tenant_provider_keys
		where account_id = $1 order by created_at desc`
	rows, err := r.pool.Query(ctx, sql, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectKeys(rows)
}

func (r *pgxRepository) ListAll(ctx context.Context) ([]Key, error) {
	sql := `select ` + selectCols + ` from public.tenant_provider_keys
		order by created_at desc`
	rows, err := r.pool.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectKeys(rows)
}

func (r *pgxRepository) Get(ctx context.Context, accountID, id uuid.UUID) (Key, error) {
	sql := `select ` + selectCols + ` from public.tenant_provider_keys
		where id = $1 and account_id = $2`
	s := &scanTarget{}
	err := r.pool.QueryRow(ctx, sql, id, accountID).Scan(s.columns()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Key{}, ErrNotFound
	}
	if err != nil {
		return Key{}, err
	}
	return s.key, nil
}

func (r *pgxRepository) Revoke(ctx context.Context, accountID, id uuid.UUID) (Key, error) {
	sql := `update public.tenant_provider_keys
		set status = 'revoked', revoked_at = now(), updated_at = now()
		where id = $2 and account_id = $1 and status = 'active'
		returning ` + selectCols

	s := &scanTarget{}
	err := r.pool.QueryRow(ctx, sql, accountID, id).Scan(s.columns()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Key{}, ErrNotFound
	}
	if err != nil {
		return Key{}, err
	}
	return s.key, nil
}

func collectKeys(rows pgx.Rows) ([]Key, error) {
	var out []Key
	for rows.Next() {
		s := &scanTarget{}
		if err := rows.Scan(s.columns()...); err != nil {
			return nil, err
		}
		out = append(out, s.key)
	}
	return out, rows.Err()
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505), same detection as the providers package.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
