package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	CreateAttempt(ctx context.Context, input StartAttemptInput) (RequestAttempt, error)
	UpdateAttemptStatus(ctx context.Context, attemptID uuid.UUID, status string, completedAt *time.Time) error
	RecordEvent(ctx context.Context, input RecordEventInput) (UsageEvent, error)
	ListAttempts(ctx context.Context, accountID uuid.UUID, requestID string, limit int) ([]RequestAttempt, error)
	ListEvents(ctx context.Context, filter ListEventsFilter) ([]UsageEvent, error)
	GetUsageSummary(ctx context.Context, filter AnalyticsFilter) ([]UsageSummaryRow, error)
	GetSpendSummary(ctx context.Context, filter AnalyticsFilter) ([]SpendSummaryRow, error)
	GetErrorSummary(ctx context.Context, filter AnalyticsFilter) ([]ErrorSummaryRow, error)
}

type pgxRepository struct {
	pool *pgxpool.Pool
}

func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

func (r *pgxRepository) CreateAttempt(ctx context.Context, input StartAttemptInput) (RequestAttempt, error) {
	customerTags, err := json.Marshal(normalizeJSONMap(input.CustomerTags))
	if err != nil {
		return RequestAttempt{}, fmt.Errorf("usage: marshal customer tags: %w", err)
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.request_attempts
			(account_id, request_id, attempt_number, endpoint, model_alias, status, user_id, team_id, service_account_id, api_key_id, customer_tags)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb)
		RETURNING id, account_id, request_id, attempt_number, endpoint, model_alias, status, user_id, team_id, service_account_id, api_key_id, customer_tags, started_at, completed_at
	`, input.AccountID, input.RequestID, input.AttemptNumber, input.Endpoint, input.ModelAlias, string(input.Status), input.UserID, input.TeamID, input.ServiceAccountID, input.APIKeyID, customerTags)

	attempt, err := scanRequestAttempt(row)
	if err != nil {
		return RequestAttempt{}, err
	}

	return attempt, nil
}

func (r *pgxRepository) UpdateAttemptStatus(ctx context.Context, attemptID uuid.UUID, status string, completedAt *time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE public.request_attempts
		SET status = $2, completed_at = $3
		WHERE id = $1
	`, attemptID, status, completedAt)
	if err != nil {
		return fmt.Errorf("usage: update attempt status: %w", err)
	}

	return nil
}

func (r *pgxRepository) RecordEvent(ctx context.Context, input RecordEventInput) (UsageEvent, error) {
	internalMetadata, err := json.Marshal(normalizeJSONMap(input.InternalMetadata))
	if err != nil {
		return UsageEvent{}, fmt.Errorf("usage: marshal internal metadata: %w", err)
	}
	customerTags, err := json.Marshal(normalizeJSONMap(input.CustomerTags))
	if err != nil {
		return UsageEvent{}, fmt.Errorf("usage: marshal customer tags: %w", err)
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.usage_events
			(account_id, request_attempt_id, api_key_id, request_id, event_type, endpoint, model_alias, status, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, hive_credit_delta, provider_request_id, internal_metadata, customer_tags, error_code, error_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb, $16::jsonb, $17, $18)
		RETURNING id, account_id, request_attempt_id, api_key_id, request_id, event_type, endpoint, model_alias, status, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, hive_credit_delta, provider_request_id, internal_metadata, customer_tags, error_code, error_type, created_at
	`, input.AccountID, input.RequestAttemptID, input.APIKeyID, input.RequestID, string(input.EventType), input.Endpoint, input.ModelAlias, input.Status, input.InputTokens, input.OutputTokens, input.CacheReadTokens, input.CacheWriteTokens, input.HiveCreditDelta, nullableString(input.ProviderRequestID), internalMetadata, customerTags, nullableString(input.ErrorCode), nullableString(input.ErrorType))

	event, err := scanUsageEvent(row)
	if err != nil {
		return UsageEvent{}, err
	}

	return event, nil
}

func (r *pgxRepository) ListAttempts(ctx context.Context, accountID uuid.UUID, requestID string, limit int) ([]RequestAttempt, error) {
	if limit <= 0 {
		limit = 20
	}

	query := `
		SELECT id, account_id, request_id, attempt_number, endpoint, model_alias, status, user_id, team_id, service_account_id, api_key_id, customer_tags, started_at, completed_at
		FROM public.request_attempts
		WHERE account_id = $1
	`
	args := []any{accountID}
	if strings.TrimSpace(requestID) != "" {
		query += ` AND request_id = $2`
		args = append(args, requestID)
		query += ` ORDER BY started_at DESC LIMIT $3`
		args = append(args, limit)
	} else {
		query += ` ORDER BY started_at DESC LIMIT $2`
		args = append(args, limit)
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("usage: list attempts: %w", err)
	}
	defer rows.Close()

	var attempts []RequestAttempt
	for rows.Next() {
		attempt, err := scanRequestAttempt(rows)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, attempt)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("usage: iterate attempts: %w", err)
	}

	return attempts, nil
}

func (r *pgxRepository) ListEvents(ctx context.Context, filter ListEventsFilter) ([]UsageEvent, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}

	query := `
		SELECT id, account_id, request_attempt_id, api_key_id, request_id, event_type, endpoint, model_alias, status, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, hive_credit_delta, provider_request_id, internal_metadata, customer_tags, error_code, error_type, created_at
		FROM public.usage_events
		WHERE account_id = $1
	`
	args := []any{filter.AccountID}
	if strings.TrimSpace(filter.RequestID) != "" {
		query += ` AND request_id = $2`
		args = append(args, filter.RequestID)
		query += ` ORDER BY created_at DESC LIMIT $3`
		args = append(args, limit)
	} else {
		query += ` ORDER BY created_at DESC LIMIT $2`
		args = append(args, limit)
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("usage: list events: %w", err)
	}
	defer rows.Close()

	var events []UsageEvent
	for rows.Next() {
		event, err := scanUsageEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("usage: iterate events: %w", err)
	}

	return events, nil
}

func (r *pgxRepository) GetUsageSummary(ctx context.Context, filter AnalyticsFilter) ([]UsageSummaryRow, error) {
	var query string
	switch filter.GroupBy {
	case "api_key":
		query = `
			SELECT api_key_id::text AS group_key,
			       SUM(input_tokens) AS total_input_tokens,
			       SUM(output_tokens) AS total_output_tokens,
			       SUM(CASE WHEN hive_credit_delta < 0 THEN ABS(hive_credit_delta) ELSE 0 END) AS total_credits_spent,
			       COUNT(*) AS request_count
			FROM public.usage_events
			WHERE account_id = $1 AND created_at >= $2 AND created_at < $3
			GROUP BY api_key_id::text
			ORDER BY total_credits_spent DESC
		`
	case "endpoint":
		query = `
			SELECT endpoint AS group_key,
			       SUM(input_tokens) AS total_input_tokens,
			       SUM(output_tokens) AS total_output_tokens,
			       SUM(CASE WHEN hive_credit_delta < 0 THEN ABS(hive_credit_delta) ELSE 0 END) AS total_credits_spent,
			       COUNT(*) AS request_count
			FROM public.usage_events
			WHERE account_id = $1 AND created_at >= $2 AND created_at < $3
			GROUP BY endpoint
			ORDER BY total_credits_spent DESC
		`
	default: // "model"
		query = `
			SELECT model_alias AS group_key,
			       SUM(input_tokens) AS total_input_tokens,
			       SUM(output_tokens) AS total_output_tokens,
			       SUM(CASE WHEN hive_credit_delta < 0 THEN ABS(hive_credit_delta) ELSE 0 END) AS total_credits_spent,
			       COUNT(*) AS request_count
			FROM public.usage_events
			WHERE account_id = $1 AND created_at >= $2 AND created_at < $3
			GROUP BY model_alias
			ORDER BY total_credits_spent DESC
		`
	}

	rows, err := r.pool.Query(ctx, query, filter.AccountID, filter.From, filter.To)
	if err != nil {
		return nil, fmt.Errorf("usage: get usage summary: %w", err)
	}
	defer rows.Close()

	var results []UsageSummaryRow
	for rows.Next() {
		var row UsageSummaryRow
		if err := rows.Scan(&row.GroupKey, &row.TotalInputTokens, &row.TotalOutputTokens, &row.TotalCreditsSpent, &row.RequestCount); err != nil {
			return nil, fmt.Errorf("usage: scan usage summary row: %w", err)
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("usage: iterate usage summary: %w", err)
	}
	return results, nil
}

func (r *pgxRepository) GetSpendSummary(ctx context.Context, filter AnalyticsFilter) ([]SpendSummaryRow, error) {
	// Spend summary is derived from usage_events (hive_credit_delta < 0) grouped by dimension.
	var query string
	switch filter.GroupBy {
	case "api_key":
		query = `
			SELECT api_key_id::text AS group_key,
			       SUM(ABS(hive_credit_delta)) AS total_credits,
			       COUNT(*) AS entry_count
			FROM public.usage_events
			WHERE account_id = $1 AND created_at >= $2 AND created_at < $3 AND hive_credit_delta < 0
			GROUP BY api_key_id::text
			ORDER BY total_credits DESC
		`
	case "endpoint":
		query = `
			SELECT endpoint AS group_key,
			       SUM(ABS(hive_credit_delta)) AS total_credits,
			       COUNT(*) AS entry_count
			FROM public.usage_events
			WHERE account_id = $1 AND created_at >= $2 AND created_at < $3 AND hive_credit_delta < 0
			GROUP BY endpoint
			ORDER BY total_credits DESC
		`
	default: // "model"
		query = `
			SELECT model_alias AS group_key,
			       SUM(ABS(hive_credit_delta)) AS total_credits,
			       COUNT(*) AS entry_count
			FROM public.usage_events
			WHERE account_id = $1 AND created_at >= $2 AND created_at < $3 AND hive_credit_delta < 0
			GROUP BY model_alias
			ORDER BY total_credits DESC
		`
	}

	rows, err := r.pool.Query(ctx, query, filter.AccountID, filter.From, filter.To)
	if err != nil {
		return nil, fmt.Errorf("usage: get spend summary: %w", err)
	}
	defer rows.Close()

	var results []SpendSummaryRow
	for rows.Next() {
		var row SpendSummaryRow
		if err := rows.Scan(&row.GroupKey, &row.TotalCredits, &row.EntryCount); err != nil {
			return nil, fmt.Errorf("usage: scan spend summary row: %w", err)
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("usage: iterate spend summary: %w", err)
	}
	return results, nil
}

func (r *pgxRepository) GetErrorSummary(ctx context.Context, filter AnalyticsFilter) ([]ErrorSummaryRow, error) {
	var query string
	switch filter.GroupBy {
	case "api_key":
		query = `
			SELECT api_key_id::text AS group_key,
			       COUNT(*) FILTER (WHERE error_code IS NOT NULL AND error_code != '') AS error_count,
			       COUNT(*) AS total_requests,
			       ROUND(COUNT(*) FILTER (WHERE error_code IS NOT NULL AND error_code != '')::numeric / NULLIF(COUNT(*), 0), 4) AS error_rate
			FROM public.usage_events
			WHERE account_id = $1 AND created_at >= $2 AND created_at < $3
			GROUP BY api_key_id::text
			ORDER BY error_count DESC
		`
	case "endpoint":
		query = `
			SELECT endpoint AS group_key,
			       COUNT(*) FILTER (WHERE error_code IS NOT NULL AND error_code != '') AS error_count,
			       COUNT(*) AS total_requests,
			       ROUND(COUNT(*) FILTER (WHERE error_code IS NOT NULL AND error_code != '')::numeric / NULLIF(COUNT(*), 0), 4) AS error_rate
			FROM public.usage_events
			WHERE account_id = $1 AND created_at >= $2 AND created_at < $3
			GROUP BY endpoint
			ORDER BY error_count DESC
		`
	default: // "model"
		query = `
			SELECT model_alias AS group_key,
			       COUNT(*) FILTER (WHERE error_code IS NOT NULL AND error_code != '') AS error_count,
			       COUNT(*) AS total_requests,
			       ROUND(COUNT(*) FILTER (WHERE error_code IS NOT NULL AND error_code != '')::numeric / NULLIF(COUNT(*), 0), 4) AS error_rate
			FROM public.usage_events
			WHERE account_id = $1 AND created_at >= $2 AND created_at < $3
			GROUP BY model_alias
			ORDER BY error_count DESC
		`
	}

	rows, err := r.pool.Query(ctx, query, filter.AccountID, filter.From, filter.To)
	if err != nil {
		return nil, fmt.Errorf("usage: get error summary: %w", err)
	}
	defer rows.Close()

	var results []ErrorSummaryRow
	for rows.Next() {
		var row ErrorSummaryRow
		if err := rows.Scan(&row.GroupKey, &row.ErrorCount, &row.TotalRequests, &row.ErrorRate); err != nil {
			return nil, fmt.Errorf("usage: scan error summary row: %w", err)
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("usage: iterate error summary: %w", err)
	}
	return results, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRequestAttempt(scanner rowScanner) (RequestAttempt, error) {
	var attempt RequestAttempt
	var customerTags []byte
	if err := scanner.Scan(
		&attempt.ID,
		&attempt.AccountID,
		&attempt.RequestID,
		&attempt.AttemptNumber,
		&attempt.Endpoint,
		&attempt.ModelAlias,
		&attempt.Status,
		&attempt.UserID,
		&attempt.TeamID,
		&attempt.ServiceAccountID,
		&attempt.APIKeyID,
		&customerTags,
		&attempt.StartedAt,
		&attempt.CompletedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return RequestAttempt{}, fmt.Errorf("usage: request attempt not found")
		}
		return RequestAttempt{}, fmt.Errorf("usage: scan request attempt: %w", err)
	}

	attempt.CustomerTags = map[string]any{}
	if len(customerTags) > 0 {
		if err := json.Unmarshal(customerTags, &attempt.CustomerTags); err != nil {
			return RequestAttempt{}, fmt.Errorf("usage: decode customer tags: %w", err)
		}
	}

	return attempt, nil
}

func scanUsageEvent(scanner rowScanner) (UsageEvent, error) {
	var event UsageEvent
	var providerRequestID *string
	var internalMetadata []byte
	var customerTags []byte
	var errorCode *string
	var errorType *string
	if err := scanner.Scan(
		&event.ID,
		&event.AccountID,
		&event.RequestAttemptID,
		&event.APIKeyID,
		&event.RequestID,
		&event.EventType,
		&event.Endpoint,
		&event.ModelAlias,
		&event.Status,
		&event.InputTokens,
		&event.OutputTokens,
		&event.CacheReadTokens,
		&event.CacheWriteTokens,
		&event.HiveCreditDelta,
		&providerRequestID,
		&internalMetadata,
		&customerTags,
		&errorCode,
		&errorType,
		&event.CreatedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return UsageEvent{}, fmt.Errorf("usage: usage event not found")
		}
		return UsageEvent{}, fmt.Errorf("usage: scan usage event: %w", err)
	}

	event.InternalMetadata = map[string]any{}
	event.CustomerTags = map[string]any{}
	if providerRequestID != nil {
		event.ProviderRequestID = *providerRequestID
	}
	if errorCode != nil {
		event.ErrorCode = *errorCode
	}
	if errorType != nil {
		event.ErrorType = *errorType
	}
	if len(internalMetadata) > 0 {
		if err := json.Unmarshal(internalMetadata, &event.InternalMetadata); err != nil {
			return UsageEvent{}, fmt.Errorf("usage: decode internal metadata: %w", err)
		}
	}
	if len(customerTags) > 0 {
		if err := json.Unmarshal(customerTags, &event.CustomerTags); err != nil {
			return UsageEvent{}, fmt.Errorf("usage: decode customer tags: %w", err)
		}
	}

	return event, nil
}

func normalizeJSONMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	return input
}

func nullableString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}
