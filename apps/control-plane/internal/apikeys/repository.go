package apikeys

import (
	"context"
	"encoding/json"
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
	UpsertPolicy(ctx context.Context, accountID, keyID uuid.UUID, input UpdatePolicyInput) (KeyPolicy, error)
	GetPolicy(ctx context.Context, accountID, keyID uuid.UUID) (KeyPolicy, error)
	ListGroupMembers(ctx context.Context, groupNames []string) ([]string, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (APIKey, error)
	GetPolicyByTokenHash(ctx context.Context, tokenHash string) (APIKey, KeyPolicy, error)
	CreateDefaultPolicy(ctx context.Context, keyID uuid.UUID) error
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

func (r *pgxRepository) UpsertPolicy(ctx context.Context, accountID, keyID uuid.UUID, input UpdatePolicyInput) (KeyPolicy, error) {
	// Verify key belongs to account first.
	if _, err := r.GetKey(ctx, accountID, keyID); err != nil {
		return KeyPolicy{}, err
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.api_key_policies
			(api_key_id, allow_all_models, allowed_group_names, allowed_aliases,
			 denied_aliases, budget_kind, budget_limit_credits, budget_anchor_at,
			 policy_version, updated_at)
		VALUES ($1,
			COALESCE($2, false),
			COALESCE($3, '["default"]'::jsonb),
			COALESCE($4, '[]'::jsonb),
			COALESCE($5, '[]'::jsonb),
			COALESCE($6, 'none'),
			$7, $8, 1, now())
		ON CONFLICT (api_key_id) DO UPDATE
		SET allow_all_models = COALESCE($2, api_key_policies.allow_all_models),
			allowed_group_names = COALESCE($3, api_key_policies.allowed_group_names),
			allowed_aliases = COALESCE($4, api_key_policies.allowed_aliases),
			denied_aliases = COALESCE($5, api_key_policies.denied_aliases),
			budget_kind = COALESCE($6, api_key_policies.budget_kind),
			budget_limit_credits = COALESCE($7, api_key_policies.budget_limit_credits),
			budget_anchor_at = COALESCE($8, api_key_policies.budget_anchor_at),
			policy_version = api_key_policies.policy_version + 1,
			updated_at = now()
		RETURNING api_key_id, allow_all_models, allowed_group_names, allowed_aliases,
			denied_aliases, budget_kind, budget_limit_credits, budget_anchor_at,
			policy_version, updated_at
	`, keyID,
		input.AllowAllModels,
		jsonbOrNil(input.AllowedGroupNames),
		jsonbOrNil(input.AllowedAliases),
		jsonbOrNil(input.DeniedAliases),
		input.BudgetKind,
		input.BudgetLimitCredits,
		input.BudgetAnchorAt,
	)

	return scanPolicy(row)
}

func (r *pgxRepository) GetPolicy(ctx context.Context, accountID, keyID uuid.UUID) (KeyPolicy, error) {
	// Verify key belongs to account first.
	if _, err := r.GetKey(ctx, accountID, keyID); err != nil {
		return KeyPolicy{}, err
	}

	row := r.pool.QueryRow(ctx, `
		SELECT api_key_id, allow_all_models, allowed_group_names, allowed_aliases,
			denied_aliases, budget_kind, budget_limit_credits, budget_anchor_at,
			policy_version, updated_at
		FROM public.api_key_policies
		WHERE api_key_id = $1
	`, keyID)

	return scanPolicy(row)
}

func (r *pgxRepository) ListGroupMembers(ctx context.Context, groupNames []string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT alias_id
		FROM public.model_policy_group_members
		WHERE group_name = ANY($1)
	`, groupNames)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var aliases []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		aliases = append(aliases, a)
	}
	return aliases, rows.Err()
}

func (r *pgxRepository) GetByTokenHash(ctx context.Context, tokenHash string) (APIKey, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, account_id, nickname, token_hash, redacted_suffix, status,
			expires_at, last_used_at, created_by_user_id, disabled_at, revoked_at,
			replaced_by_key_id, created_at, updated_at
		FROM public.api_keys
		WHERE token_hash = $1
	`, tokenHash)

	return scanKey(row)
}

func (r *pgxRepository) GetPolicyByTokenHash(ctx context.Context, tokenHash string) (APIKey, KeyPolicy, error) {
	key, err := r.GetByTokenHash(ctx, tokenHash)
	if err != nil {
		return APIKey{}, KeyPolicy{}, err
	}

	row := r.pool.QueryRow(ctx, `
		SELECT api_key_id, allow_all_models, allowed_group_names, allowed_aliases,
			denied_aliases, budget_kind, budget_limit_credits, budget_anchor_at,
			policy_version, updated_at
		FROM public.api_key_policies
		WHERE api_key_id = $1
	`, key.ID)

	policy, err := scanPolicy(row)
	if err != nil {
		// Key exists but has no policy row — return default.
		if err == ErrNotFound {
			policy = KeyPolicy{
				APIKeyID:          key.ID,
				AllowedGroupNames: []string{"default"},
				BudgetKind:        "none",
				PolicyVersion:     1,
			}
			return key, policy, nil
		}
		return APIKey{}, KeyPolicy{}, err
	}

	return key, policy, nil
}

func (r *pgxRepository) CreateDefaultPolicy(ctx context.Context, keyID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO public.api_key_policies (api_key_id)
		VALUES ($1)
		ON CONFLICT (api_key_id) DO NOTHING
	`, keyID)
	return err
}

func scanPolicy(row scannable) (KeyPolicy, error) {
	var p KeyPolicy
	var groupNames, allowedAliases, deniedAliases []byte
	err := row.Scan(
		&p.APIKeyID, &p.AllowAllModels, &groupNames, &allowedAliases,
		&deniedAliases, &p.BudgetKind, &p.BudgetLimitCredits, &p.BudgetAnchorAt,
		&p.PolicyVersion, &p.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return KeyPolicy{}, ErrNotFound
		}
		return KeyPolicy{}, err
	}
	p.AllowedGroupNames = parseStringSlice(groupNames)
	p.AllowedAliases = parseStringSlice(allowedAliases)
	p.DeniedAliases = parseStringSlice(deniedAliases)
	return p, nil
}

func parseStringSlice(data []byte) []string {
	if data == nil {
		return nil
	}
	var result []string
	_ = json.Unmarshal(data, &result)
	return result
}

func jsonbOrNil(values []string) interface{} {
	if values == nil {
		return nil
	}
	data, _ := json.Marshal(values)
	return string(data)
}
