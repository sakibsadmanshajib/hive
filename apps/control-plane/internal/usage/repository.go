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
			(account_id, request_attempt_id, request_id, event_type, endpoint, model_alias, status, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, hive_credit_delta, provider_request_id, internal_metadata, customer_tags, error_code, error_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb, $15::jsonb, $16, $17)
		RETURNING id, account_id, request_attempt_id, request_id, event_type, endpoint, model_alias, status, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, hive_credit_delta, provider_request_id, internal_metadata, customer_tags, error_code, error_type, created_at
	`, input.AccountID, input.RequestAttemptID, input.RequestID, string(input.EventType), input.Endpoint, input.ModelAlias, input.Status, input.InputTokens, input.OutputTokens, input.CacheReadTokens, input.CacheWriteTokens, input.HiveCreditDelta, nullableString(input.ProviderRequestID), internalMetadata, customerTags, nullableString(input.ErrorCode), nullableString(input.ErrorType))

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
		SELECT id, account_id, request_attempt_id, request_id, event_type, endpoint, model_alias, status, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, hive_credit_delta, provider_request_id, internal_metadata, customer_tags, error_code, error_type, created_at
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
