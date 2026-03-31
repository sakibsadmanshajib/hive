package apikeys

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository defines the data-access interface for API keys.
type Repository interface {
	CreateKey(ctx context.Context, key APIKey) (APIKey, error)
	GetKey(ctx context.Context, accountID, keyID uuid.UUID) (APIKey, error)
	ListKeys(ctx context.Context, accountID uuid.UUID) ([]APIKey, error)
	UpdateKeyState(ctx context.Context, accountID, keyID uuid.UUID, status KeyStatus, disabledAt, revokedAt *time.Time, replacedBy *uuid.UUID) (APIKey, error)
	InsertEvent(ctx context.Context, event KeyEvent) error
	CreateReplacementKey(ctx context.Context, oldKeyID uuid.UUID, newKey APIKey, rotatedAt time.Time) (APIKey, APIKey, error)
}

// pgxRepository is the production implementation backed by Supabase Postgres.
type pgxRepository struct {
	pool *pgxpool.Pool
}

// NewPgxRepository returns a Repository backed by the given pgx pool.
func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

func (r *pgxRepository) CreateKey(ctx context.Context, key APIKey) (APIKey, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.api_keys
			(id, account_id, nickname, token_hash, redacted_suffix, status,
			 expires_at, created_by_user_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now(), now())
		RETURNING id, account_id, nickname, token_hash, redacted_suffix, status,
			expires_at, last_used_at, created_by_user_id, disabled_at, revoked_at,
			replaced_by_key_id, created_at, updated_at
	`, key.ID, key.AccountID, key.Nickname, key.TokenHash, key.RedactedSuffix,
		string(key.Status), key.ExpiresAt, key.CreatedByUserID)

	return scanKey(row)
}

func (r *pgxRepository) GetKey(ctx context.Context, accountID, keyID uuid.UUID) (APIKey, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, account_id, nickname, token_hash, redacted_suffix, status,
			expires_at, last_used_at, created_by_user_id, disabled_at, revoked_at,
			replaced_by_key_id, created_at, updated_at
		FROM public.api_keys
		WHERE id = $1 AND account_id = $2
	`, keyID, accountID)

	return scanKey(row)
}

func (r *pgxRepository) ListKeys(ctx context.Context, accountID uuid.UUID) ([]APIKey, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, account_id, nickname, token_hash, redacted_suffix, status,
			expires_at, last_used_at, created_by_user_id, disabled_at, revoked_at,
			replaced_by_key_id, created_at, updated_at
		FROM public.api_keys
		WHERE account_id = $1
		ORDER BY created_at DESC
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (r *pgxRepository) UpdateKeyState(ctx context.Context, accountID, keyID uuid.UUID, status KeyStatus, disabledAt, revokedAt *time.Time, replacedBy *uuid.UUID) (APIKey, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE public.api_keys
		SET status = $1, disabled_at = $2, revoked_at = $3, replaced_by_key_id = $4, updated_at = now()
		WHERE id = $5 AND account_id = $6
		RETURNING id, account_id, nickname, token_hash, redacted_suffix, status,
			expires_at, last_used_at, created_by_user_id, disabled_at, revoked_at,
			replaced_by_key_id, created_at, updated_at
	`, string(status), disabledAt, revokedAt, replacedBy, keyID, accountID)

	return scanKey(row)
}

func (r *pgxRepository) InsertEvent(ctx context.Context, event KeyEvent) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO public.api_key_events
			(id, api_key_id, account_id, event_type, actor_user_id, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
	`, event.ID, event.APIKeyID, event.AccountID, event.EventType, event.ActorUserID, event.Metadata)
	return err
}

func (r *pgxRepository) CreateReplacementKey(ctx context.Context, oldKeyID uuid.UUID, newKey APIKey, rotatedAt time.Time) (APIKey, APIKey, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return APIKey{}, APIKey{}, err
	}
	defer tx.Rollback(ctx)

	// Insert the new replacement key.
	newRow := tx.QueryRow(ctx, `
		INSERT INTO public.api_keys
			(id, account_id, nickname, token_hash, redacted_suffix, status,
			 expires_at, created_by_user_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now(), now())
		RETURNING id, account_id, nickname, token_hash, redacted_suffix, status,
			expires_at, last_used_at, created_by_user_id, disabled_at, revoked_at,
			replaced_by_key_id, created_at, updated_at
	`, newKey.ID, newKey.AccountID, newKey.Nickname, newKey.TokenHash, newKey.RedactedSuffix,
		string(newKey.Status), newKey.ExpiresAt, newKey.CreatedByUserID)

	created, err := scanKey(newRow)
	if err != nil {
		return APIKey{}, APIKey{}, err
	}

	// Revoke the old key and link it to the replacement.
	oldRow := tx.QueryRow(ctx, `
		UPDATE public.api_keys
		SET status = 'revoked', revoked_at = $1, replaced_by_key_id = $2, updated_at = now()
		WHERE id = $3
		RETURNING id, account_id, nickname, token_hash, redacted_suffix, status,
			expires_at, last_used_at, created_by_user_id, disabled_at, revoked_at,
			replaced_by_key_id, created_at, updated_at
	`, rotatedAt, created.ID, oldKeyID)

	old, err := scanKey(oldRow)
	if err != nil {
		return APIKey{}, APIKey{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return APIKey{}, APIKey{}, err
	}

	return old, created, nil
}

// scannable is satisfied by both pgx.Row and pgx.Rows.
type scannable interface {
	Scan(dest ...interface{}) error
}

func scanKey(row scannable) (APIKey, error) {
	var k APIKey
	var status string
	err := row.Scan(
		&k.ID, &k.AccountID, &k.Nickname, &k.TokenHash, &k.RedactedSuffix, &status,
		&k.ExpiresAt, &k.LastUsedAt, &k.CreatedByUserID, &k.DisabledAt, &k.RevokedAt,
		&k.ReplacedByKeyID, &k.CreatedAt, &k.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return APIKey{}, ErrNotFound
		}
		return APIKey{}, err
	}
	k.Status = KeyStatus(status)
	return k, nil
}
