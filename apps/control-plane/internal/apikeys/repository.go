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
	GetKeyByID(ctx context.Context, keyID uuid.UUID) (APIKey, error)
	GetKey(ctx context.Context, accountID, keyID uuid.UUID) (APIKey, error)
	ListKeys(ctx context.Context, accountID uuid.UUID) ([]APIKey, error)
	UpdateKeyState(ctx context.Context, accountID, keyID uuid.UUID, status KeyStatus, disabledAt, revokedAt *time.Time, replacedBy *uuid.UUID) (APIKey, error)
	InsertEvent(ctx context.Context, event KeyEvent) error
	CreateReplacementKey(ctx context.Context, oldKeyID uuid.UUID, newKey APIKey, rotatedAt time.Time) (APIKey, APIKey, error)
	UpsertPolicy(ctx context.Context, accountID, keyID uuid.UUID, input UpdatePolicyInput) (KeyPolicy, error)
	GetPolicy(ctx context.Context, accountID, keyID uuid.UUID) (KeyPolicy, error)
	ListPolicies(ctx context.Context, accountID uuid.UUID) ([]KeyPolicy, error)
	ListGroupMembers(ctx context.Context, groupNames []string) ([]string, error)
	ListAllAliases(ctx context.Context) ([]string, error)
	GetBudgetWindow(ctx context.Context, apiKeyID uuid.UUID, budgetKind string, at time.Time) (BudgetWindow, error)
	GetKeyRatePolicy(ctx context.Context, apiKeyID uuid.UUID) (RatePolicy, error)
	GetAccountRatePolicy(ctx context.Context, accountID uuid.UUID) (RatePolicy, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (APIKey, error)
	GetPolicyByTokenHash(ctx context.Context, tokenHash string) (APIKey, KeyPolicy, error)
	CreateDefaultPolicy(ctx context.Context, keyID uuid.UUID) error

	ApplyReservationDelta(ctx context.Context, apiKeyID uuid.UUID, budgetKind string, reservedDelta int64, consumedDelta int64, at time.Time) error
	RecordUsageFinalization(ctx context.Context, apiKeyID uuid.UUID, modelAlias string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, consumedCredits int64, at time.Time) error
	MarkLastUsed(ctx context.Context, apiKeyID uuid.UUID, usedAt time.Time) error
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

func (r *pgxRepository) GetKeyByID(ctx context.Context, keyID uuid.UUID) (APIKey, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, account_id, nickname, token_hash, redacted_suffix, status,
			expires_at, last_used_at, created_by_user_id, disabled_at, revoked_at,
			replaced_by_key_id, created_at, updated_at
		FROM public.api_keys
		WHERE id = $1
	`, keyID)

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

func (r *pgxRepository) ListPolicies(ctx context.Context, accountID uuid.UUID) ([]KeyPolicy, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT p.api_key_id, p.allow_all_models, p.allowed_group_names, p.allowed_aliases,
			p.denied_aliases, p.budget_kind, p.budget_limit_credits, p.budget_anchor_at,
			p.policy_version, p.updated_at
		FROM public.api_key_policies p
		JOIN public.api_keys k ON k.id = p.api_key_id
		WHERE k.account_id = $1
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []KeyPolicy
	for rows.Next() {
		policy, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	return policies, rows.Err()
}

func (r *pgxRepository) ListGroupMembers(ctx context.Context, groupNames []string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT alias_id
		FROM (
			SELECT m.alias_id,
				MIN(array_position($1::text[], m.group_name)) AS group_order,
				MIN(a.created_at) AS alias_created_at
			FROM public.model_policy_group_members m
			JOIN public.model_aliases a ON a.alias_id = m.alias_id
			WHERE m.group_name = ANY($1)
			GROUP BY m.alias_id
		) resolved
		ORDER BY group_order ASC, alias_created_at ASC, alias_id ASC
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

func (r *pgxRepository) ListAllAliases(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT alias_id
		FROM public.model_aliases
		ORDER BY created_at ASC, alias_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var aliases []string
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return nil, err
		}
		aliases = append(aliases, alias)
	}
	return aliases, rows.Err()
}

func (r *pgxRepository) GetBudgetWindow(ctx context.Context, apiKeyID uuid.UUID, budgetKind string, at time.Time) (BudgetWindow, error) {
	window := BudgetWindow{
		APIKeyID:    apiKeyID,
		WindowKind:  budgetKind,
		WindowStart: time.Time{},
	}
	if budgetKind == "" || budgetKind == "none" {
		return window, nil
	}
	if budgetKind == "monthly" {
		window.WindowStart = time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	}

	row := r.pool.QueryRow(ctx, `
		SELECT api_key_id, window_kind, window_start, window_end, consumed_credits, reserved_credits, updated_at
		FROM public.api_key_budget_windows
		WHERE api_key_id = $1 AND window_kind = $2 AND window_start = $3
	`, apiKeyID, budgetKind, window.WindowStart)

	err := row.Scan(
		&window.APIKeyID,
		&window.WindowKind,
		&window.WindowStart,
		&window.WindowEnd,
		&window.ConsumedCredits,
		&window.ReservedCredits,
		&window.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return window, nil
	}
	if err != nil {
		return BudgetWindow{}, err
	}
	return window, nil
}

func (r *pgxRepository) GetKeyRatePolicy(ctx context.Context, apiKeyID uuid.UUID) (RatePolicy, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT requests_per_minute, tokens_per_minute, rolling_five_hour_limit, weekly_limit, free_token_weight_tenths
		FROM public.api_key_rate_policies
		WHERE api_key_id = $1
	`, apiKeyID)
	policy, err := scanRatePolicy(row)
	if err == ErrNotFound {
		return defaultRatePolicy(), nil
	}
	return policy, err
}

func (r *pgxRepository) GetAccountRatePolicy(ctx context.Context, accountID uuid.UUID) (RatePolicy, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT requests_per_minute, tokens_per_minute, rolling_five_hour_limit, weekly_limit, free_token_weight_tenths
		FROM public.account_rate_policies
		WHERE account_id = $1
	`, accountID)
	policy, err := scanRatePolicy(row)
	if err == ErrNotFound {
		return defaultRatePolicy(), nil
	}
	return policy, err
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

func (r *pgxRepository) ApplyReservationDelta(ctx context.Context, apiKeyID uuid.UUID, budgetKind string, reservedDelta int64, consumedDelta int64, at time.Time) error {
	var start time.Time
	if budgetKind == "monthly" {
		start = time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	}

	_, err := r.pool.Exec(ctx, `
		INSERT INTO public.api_key_budget_windows (api_key_id, window_kind, window_start, reserved_credits, consumed_credits)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (api_key_id, window_kind, window_start) DO UPDATE
		SET reserved_credits = public.api_key_budget_windows.reserved_credits + EXCLUDED.reserved_credits,
		    consumed_credits = public.api_key_budget_windows.consumed_credits + EXCLUDED.consumed_credits,
		    updated_at = now()
	`, apiKeyID, budgetKind, start, reservedDelta, consumedDelta)
	return err
}

func (r *pgxRepository) RecordUsageFinalization(ctx context.Context, apiKeyID uuid.UUID, modelAlias string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, consumedCredits int64, at time.Time) error {
	monthlyStart := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	var lifetimeStart time.Time

	query := `
		INSERT INTO public.api_key_usage_rollups
			(api_key_id, model_alias, window_kind, window_start, request_count, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, consumed_credits, last_seen_at)
		VALUES ($1, $2, $3, $4, 1, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (api_key_id, model_alias, window_kind, window_start) DO UPDATE
		SET request_count = public.api_key_usage_rollups.request_count + 1,
		    input_tokens = public.api_key_usage_rollups.input_tokens + EXCLUDED.input_tokens,
		    output_tokens = public.api_key_usage_rollups.output_tokens + EXCLUDED.output_tokens,
		    cache_read_tokens = public.api_key_usage_rollups.cache_read_tokens + EXCLUDED.cache_read_tokens,
		    cache_write_tokens = public.api_key_usage_rollups.cache_write_tokens + EXCLUDED.cache_write_tokens,
		    consumed_credits = public.api_key_usage_rollups.consumed_credits + EXCLUDED.consumed_credits,
		    last_seen_at = GREATEST(public.api_key_usage_rollups.last_seen_at, EXCLUDED.last_seen_at)
	`

	if _, err := r.pool.Exec(ctx, query, apiKeyID, modelAlias, "monthly", monthlyStart, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, consumedCredits, at); err != nil {
		return err
	}
	if _, err := r.pool.Exec(ctx, query, apiKeyID, modelAlias, "lifetime", lifetimeStart, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, consumedCredits, at); err != nil {
		return err
	}
	return nil
}

func (r *pgxRepository) MarkLastUsed(ctx context.Context, apiKeyID uuid.UUID, usedAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE public.api_keys
		SET last_used_at = GREATEST(COALESCE(last_used_at, '1970-01-01'::timestamptz), $2), updated_at = now()
		WHERE id = $1
	`, apiKeyID, usedAt)
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

func scanRatePolicy(row scannable) (RatePolicy, error) {
	var policy RatePolicy
	if err := row.Scan(
		&policy.RateLimitRPM,
		&policy.RateLimitTPM,
		&policy.RollingFiveHourLimit,
		&policy.WeeklyLimit,
		&policy.FreeTokenWeightTenths,
	); err != nil {
		if err == pgx.ErrNoRows {
			return RatePolicy{}, ErrNotFound
		}
		return RatePolicy{}, err
	}
	return policy, nil
}

func defaultRatePolicy() RatePolicy {
	return RatePolicy{
		RateLimitRPM:          60,
		RateLimitTPM:          120000,
		RollingFiveHourLimit:  0,
		WeeklyLimit:           0,
		FreeTokenWeightTenths: 1,
	}
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
