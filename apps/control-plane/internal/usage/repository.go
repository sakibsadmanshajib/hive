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

	// Idempotent on (account_id, request_id, attempt_number). A single request
	// starts the same attempt twice: the edge-api orchestrator calls StartAttempt
	// directly, and the reservation path (accounting.CreateReservation) starts it
	// again to link the hold. A plain INSERT collided on
	// idx_request_attempts_account_request_attempt (23505), which aborted the
	// reservation transaction and silently dropped the credit hold. ON CONFLICT
	// returns the pre-existing attempt unchanged (first write wins); the no-op SET
	// is only there so RETURNING yields the existing row. Status transitions stay
	// owned by UpdateAttemptStatus, so this never moves an attempt backward.
	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.request_attempts
			(account_id, request_id, attempt_number, endpoint, model_alias, status, user_id, team_id, service_account_id, api_key_id, customer_tags)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb)
		ON CONFLICT (account_id, request_id, attempt_number)
		DO UPDATE SET status = request_attempts.status
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

	// ON CONFLICT targets ux_usage_events_completed_attempt (partial unique
	// index on (account_id, request_attempt_id) WHERE event_type =
	// 'completed', supabase/migrations/20260825_03_...sql). Only a
	// 'completed' insert can ever hit this arbiter; every other event_type
	// inserts exactly as before.
	//
	// Two writers record a 'completed' row for the same attempt today
	// (issue #1180): control-plane's own accounting.finalizeLocked, which
	// always runs FIRST and carries the ledger-matching hive_credit_delta,
	// and edge-api's separate, unconditional POST that always runs SECOND
	// and carries the real measured token counts but a hive_credit_delta
	// that is not a credit figure at all (it is edge-api's TotalTokens,
	// unrelated to this fix's scope). DO UPDATE folds the second write's
	// token/provider columns onto the first write's row and deliberately
	// never touches hive_credit_delta, event_type, status, or the account/
	// request identifiers: the row that lands first is presumed
	// authoritative on the money question, and every case measured live
	// (576 of 576 duplicate pairs, 2026-08-25) bears that out.
	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.usage_events
			(account_id, request_attempt_id, api_key_id, request_id, event_type, endpoint, model_alias, status, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, hive_credit_delta, provider_request_id, internal_metadata, customer_tags, error_code, error_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb, $16::jsonb, $17, $18)
		ON CONFLICT (account_id, request_attempt_id) WHERE event_type = 'completed'
		DO UPDATE SET
			input_tokens = EXCLUDED.input_tokens,
			output_tokens = EXCLUDED.output_tokens,
			cache_read_tokens = EXCLUDED.cache_read_tokens,
			cache_write_tokens = EXCLUDED.cache_write_tokens,
			provider_request_id = COALESCE(EXCLUDED.provider_request_id, public.usage_events.provider_request_id),
			internal_metadata = public.usage_events.internal_metadata || EXCLUDED.internal_metadata
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

	// Every predicate is parameterized; the placeholder numbers are derived
	// from the running arg count so no user-controlled value is ever
	// interpolated into the SQL text.
	//
	// LEFT JOINed against request_attempts for latency_ms only: the attempt
	// row an event's request_attempt_id references (NOT NULL, FK-enforced),
	// so the join never drops a usage_events row, it only ever adds one
	// nullable column. latency_ms is NULL until the attempt has a
	// completed_at (UpdateAttemptStatus sets it on every terminal outcome in
	// accounting.FinalizeReservation), so an in-flight or still-streaming
	// request reports latency as genuinely unknown rather than a fabricated
	// zero. Every bare column in both SELECT and WHERE is prefixed ue./ra.
	// because request_id, model_alias, api_key_id and status exist on both
	// tables and an unprefixed reference is ambiguous once the join is in
	// the query.
	query := `
		SELECT ue.id, ue.account_id, ue.request_attempt_id, ue.api_key_id, ue.request_id, ue.event_type, ue.endpoint, ue.model_alias, ue.status, ue.input_tokens, ue.output_tokens, ue.cache_read_tokens, ue.cache_write_tokens, ue.hive_credit_delta, ue.provider_request_id, ue.internal_metadata, ue.customer_tags, ue.error_code, ue.error_type, ue.created_at,
		       CASE WHEN ra.completed_at IS NULL THEN NULL
		            ELSE ROUND(EXTRACT(EPOCH FROM (ra.completed_at - ra.started_at)) * 1000)::bigint
		       END AS latency_ms
		FROM public.usage_events ue
		LEFT JOIN public.request_attempts ra ON ra.id = ue.request_attempt_id
		WHERE ue.account_id = $1
	`
	args := []any{filter.AccountID}
	next := func() int { return len(args) + 1 }

	if v := strings.TrimSpace(filter.RequestID); v != "" {
		query += fmt.Sprintf(` AND ue.request_id = $%d`, next())
		args = append(args, v)
	}
	if v := strings.TrimSpace(filter.ModelAlias); v != "" {
		query += fmt.Sprintf(` AND ue.model_alias = $%d`, next())
		args = append(args, v)
	}
	if filter.APIKeyID != nil {
		query += fmt.Sprintf(` AND ue.api_key_id = $%d`, next())
		args = append(args, *filter.APIKeyID)
	}
	if v := strings.TrimSpace(filter.Status); v != "" {
		query += fmt.Sprintf(` AND ue.status = $%d`, next())
		args = append(args, v)
	}
	if filter.ErrorsOnly {
		query += ` AND ue.error_code IS NOT NULL AND ue.error_code <> ''`
	}
	if !filter.From.IsZero() {
		query += fmt.Sprintf(` AND ue.created_at >= $%d`, next())
		args = append(args, filter.From)
	}
	if !filter.To.IsZero() {
		query += fmt.Sprintf(` AND ue.created_at < $%d`, next())
		args = append(args, filter.To)
	}
	// Keyset pagination: the cursor is the id of the last row on the previous
	// page. The subquery resolves that row's (created_at, id) pair so the
	// row-value comparison continues the same created_at DESC, id DESC order
	// the page was read in. A cursor whose row has since been deleted yields
	// an empty (not a wrong) page.
	if filter.CursorID != nil && *filter.CursorID != uuid.Nil {
		query += fmt.Sprintf(` AND (ue.created_at, ue.id) < (SELECT created_at, id FROM public.usage_events WHERE id = $%d)`, next())
		args = append(args, *filter.CursorID)
	}

	query += fmt.Sprintf(` ORDER BY ue.created_at DESC, ue.id DESC LIMIT $%d`, next())
	args = append(args, limit)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("usage: list events: %w", err)
	}
	defer rows.Close()

	var events []UsageEvent
	for rows.Next() {
		event, err := scanUsageEventWithLatency(rows)
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
		var groupKey *string
		if err := rows.Scan(&groupKey, &row.TotalInputTokens, &row.TotalOutputTokens, &row.TotalCreditsSpent, &row.RequestCount); err != nil {
			return nil, fmt.Errorf("usage: scan usage summary row: %w", err)
		}
		row.GroupKey = groupKeyOrUnattributed(groupKey)
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
		var groupKey *string
		if err := rows.Scan(&groupKey, &row.TotalCredits, &row.EntryCount); err != nil {
			return nil, fmt.Errorf("usage: scan spend summary row: %w", err)
		}
		row.GroupKey = groupKeyOrUnattributed(groupKey)
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
		var groupKey *string
		if err := rows.Scan(&groupKey, &row.ErrorCount, &row.TotalRequests, &row.ErrorRate); err != nil {
			return nil, fmt.Errorf("usage: scan error summary row: %w", err)
		}
		row.GroupKey = groupKeyOrUnattributed(groupKey)
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
	return scanUsageEventRow(scanner, false)
}

// scanUsageEventWithLatency scans a ListEvents row, which carries one extra
// trailing column (latency_ms) that RecordEvent's plain RETURNING clause
// does not project. Kept as a separate entry point rather than a variadic
// scanUsageEvent so a caller's column list and its scan target list always
// match in count, which pgx enforces at Scan time.
func scanUsageEventWithLatency(scanner rowScanner) (UsageEvent, error) {
	return scanUsageEventRow(scanner, true)
}

func scanUsageEventRow(scanner rowScanner, withLatency bool) (UsageEvent, error) {
	var event UsageEvent
	var providerRequestID *string
	var internalMetadata []byte
	var customerTags []byte
	var errorCode *string
	var errorType *string
	var latencyMs *int64

	dest := []any{
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
	}
	if withLatency {
		dest = append(dest, &latencyMs)
	}

	if err := scanner.Scan(dest...); err != nil {
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
	event.LatencyMs = latencyMs
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

// UnattributedGroupKey is the group_key the analytics summaries report for
// rows whose grouping column is NULL (issue #1347).
//
// usage_events.api_key_id is the only nullable grouping column: endpoint and
// model_alias are both NOT NULL, so group_by=model and group_by=endpoint can
// never produce this bucket. It goes NULL for two real reasons, and neither
// is a data defect: the request failed before a key was resolved, and the FK
// is ON DELETE SET NULL, so an attributed row loses its key when that key is
// deleted. Those rows are aggregated here rather than dropped, because a
// pre-auth error is exactly the kind an operator most wants to see, and a
// silent undercount is a worse failure than a visible bucket. The literal
// cannot collide with a real group key, which renders as a UUID.
const UnattributedGroupKey = "unattributed"

// groupKeyOrUnattributed collapses a nullable group_key scan destination.
// Scanning straight into a string instead failed the whole summary query on
// the first unattributable row, which the console rendered as a 500.
func groupKeyOrUnattributed(raw *string) string {
	if raw == nil {
		return UnattributedGroupKey
	}
	return *raw
}
