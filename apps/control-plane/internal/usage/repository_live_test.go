package usage

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/packages/dbtest"
)

// Behavioral coverage for the usage repository against the real Postgres
// schema. Before this file, only CreateAttempt's idempotency was exercised
// live (repository_test.go); RecordEvent, ListEvents, GetUsageSummary,
// GetSpendSummary, GetErrorSummary and UpdateAttemptStatus all ran at 0%
// against real SQL, which is exactly the analytics/rollup surface a mock
// cannot catch a broken GROUP BY or a dropped column on.
//
// Cache-token contract documented here (live context: PR #1173 fixed
// hardcoded zero input/output tokens in the accounting rollup; issue #1174
// tracks cache tokens still writing zero because no finalize path carries
// them). This suite does NOT fix #1174 — that is cross-service plumbing
// outside this package — but it pins down, against real SQL, exactly which
// part of usage's own contract already carries cache tokens (the raw
// RecordEvent/ListEvents round trip) and which part does not
// (GetUsageSummary's rollup), so a future fix has a guard that goes red the
// moment either boundary moves.

func newUsageTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return dbtest.Pool(t, "HIVE_TEST_DB_URL")
}

// seedUsageAccount inserts an auth.users row and the public.accounts row it
// owns, registering cleanup that cascades usage_events/request_attempts.
func seedUsageAccount(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO auth.users (id, email, raw_user_meta_data)
		 VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		"usage-repo-"+uuid.NewString()+"@test.local",
	).Scan(&userID); err != nil {
		t.Fatalf("seed auth.users: %v", err)
	}

	var accountID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO public.accounts (id, slug, display_name, account_type, owner_user_id)
		 VALUES (gen_random_uuid(), $1, 'usage repo test', 'personal', $2) RETURNING id`,
		"usage-repo-"+uuid.NewString(), userID,
	).Scan(&accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM public.accounts WHERE id = $1`, accountID)
		_, _ = pool.Exec(c, `DELETE FROM auth.users WHERE id = $1`, userID)
	})
	return accountID
}

func seedUsageAttempt(t *testing.T, ctx context.Context, repo Repository, accountID uuid.UUID) uuid.UUID {
	t.Helper()
	attempt, err := repo.CreateAttempt(ctx, StartAttemptInput{
		AccountID:     accountID,
		RequestID:     "req-" + uuid.NewString(),
		AttemptNumber: 1,
		Endpoint:      "/v1/chat/completions",
		ModelAlias:    "gpt-test",
		Status:        AttemptStatusDispatching,
		CustomerTags:  map[string]any{},
	})
	if err != nil {
		t.Fatalf("seed attempt: %v", err)
	}
	return attempt.ID
}

// -----------------------------------------------------------------------------
// RecordEvent + ListEvents: cache tokens survive the raw event round trip.
// -----------------------------------------------------------------------------

func TestRecordEventAndListEvents_CacheTokensRoundTrip(t *testing.T) {
	pool := newUsageTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()
	accountID := seedUsageAccount(t, pool)
	attemptID := seedUsageAttempt(t, ctx, repo, accountID)

	requestID := "req-" + uuid.NewString()
	written, err := repo.RecordEvent(ctx, RecordEventInput{
		AccountID:        accountID,
		RequestAttemptID: attemptID,
		RequestID:        requestID,
		EventType:        UsageEventCompleted,
		Endpoint:         "/v1/chat/completions",
		ModelAlias:       "gpt-test",
		Status:           "completed",
		InputTokens:      100,
		OutputTokens:     50,
		CacheReadTokens:  30,
		CacheWriteTokens: 20,
		HiveCreditDelta:  -1234,
		InternalMetadata: map[string]any{},
		CustomerTags:     map[string]any{},
	})
	if err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}
	if written.CacheReadTokens != 30 || written.CacheWriteTokens != 20 {
		t.Fatalf("RecordEvent's own return must carry cache tokens: got read=%d write=%d",
			written.CacheReadTokens, written.CacheWriteTokens)
	}

	events, err := repo.ListEvents(ctx, ListEventsFilter{AccountID: accountID, RequestID: requestID, Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event, got %d", len(events))
	}
	got := events[0]
	if got.InputTokens != 100 || got.OutputTokens != 50 {
		t.Fatalf("expected input/output tokens to round-trip, got input=%d output=%d", got.InputTokens, got.OutputTokens)
	}
	if got.CacheReadTokens != 30 || got.CacheWriteTokens != 20 {
		t.Fatalf("ListEvents must return the cache tokens RecordEvent stored: got read=%d write=%d, want read=30 write=20",
			got.CacheReadTokens, got.CacheWriteTokens)
	}
}

func TestRecordEventAndListEvents_ZeroCacheTokensStayZero(t *testing.T) {
	// The inverse of the round-trip test: an event that genuinely has no
	// cache activity must read back as zero, not as some other sentinel and
	// not as a value borrowed from input/output tokens.
	pool := newUsageTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()
	accountID := seedUsageAccount(t, pool)
	attemptID := seedUsageAttempt(t, ctx, repo, accountID)

	requestID := "req-" + uuid.NewString()
	if _, err := repo.RecordEvent(ctx, RecordEventInput{
		AccountID:        accountID,
		RequestAttemptID: attemptID,
		RequestID:        requestID,
		EventType:        UsageEventCompleted,
		Endpoint:         "/v1/chat/completions",
		ModelAlias:       "gpt-test",
		Status:           "completed",
		InputTokens:      10,
		OutputTokens:     5,
		HiveCreditDelta:  -1,
		InternalMetadata: map[string]any{},
		CustomerTags:     map[string]any{},
	}); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}

	events, err := repo.ListEvents(ctx, ListEventsFilter{AccountID: accountID, RequestID: requestID, Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event, got %d", len(events))
	}
	if events[0].CacheReadTokens != 0 || events[0].CacheWriteTokens != 0 {
		t.Fatalf("expected untouched cache tokens to read back as zero, got read=%d write=%d",
			events[0].CacheReadTokens, events[0].CacheWriteTokens)
	}
}

// -----------------------------------------------------------------------------
// ListEvents: latency_ms is derived from request_attempts.started_at /
// completed_at, not stored on usage_events itself. Console's request log
// depends on this being present once an attempt terminates and absent
// (nil, never a fabricated zero) while it has not.
// -----------------------------------------------------------------------------

func TestListEvents_LatencyMsPresentAfterAttemptCompletes(t *testing.T) {
	pool := newUsageTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()
	accountID := seedUsageAccount(t, pool)
	attemptID := seedUsageAttempt(t, ctx, repo, accountID)

	// UpdateAttemptStatus is what accounting.FinalizeReservation calls on
	// every terminal outcome (service.go:392), so this mirrors production
	// rather than reaching into the schema directly.
	completedAt := time.Now().UTC().Add(750 * time.Millisecond)
	if err := repo.UpdateAttemptStatus(ctx, attemptID, "completed", &completedAt); err != nil {
		t.Fatalf("UpdateAttemptStatus: %v", err)
	}

	requestID := "req-" + uuid.NewString()
	if _, err := repo.RecordEvent(ctx, RecordEventInput{
		AccountID:        accountID,
		RequestAttemptID: attemptID,
		RequestID:        requestID,
		EventType:        UsageEventCompleted,
		Endpoint:         "/v1/chat/completions",
		ModelAlias:       "gpt-test",
		Status:           "completed",
		HiveCreditDelta:  -1,
		InternalMetadata: map[string]any{},
		CustomerTags:     map[string]any{},
	}); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}

	events, err := repo.ListEvents(ctx, ListEventsFilter{AccountID: accountID, RequestID: requestID, Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event, got %d", len(events))
	}
	if events[0].LatencyMs == nil {
		t.Fatal("expected latency_ms to be populated once the attempt has completed_at, got nil")
	}
	if *events[0].LatencyMs < 500 || *events[0].LatencyMs > 5000 {
		t.Fatalf("expected latency_ms close to the seeded ~750ms gap, got %d", *events[0].LatencyMs)
	}
}

func TestListEvents_LatencyMsNilWhileAttemptInFlight(t *testing.T) {
	// The inverse: an attempt that never got a completed_at (still
	// dispatching/streaming) must read back latency as nil, never as a
	// fabricated zero that would misreport a genuinely unknown duration as
	// an instant response.
	pool := newUsageTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()
	accountID := seedUsageAccount(t, pool)
	attemptID := seedUsageAttempt(t, ctx, repo, accountID)

	requestID := "req-" + uuid.NewString()
	if _, err := repo.RecordEvent(ctx, RecordEventInput{
		AccountID:        accountID,
		RequestAttemptID: attemptID,
		RequestID:        requestID,
		EventType:        UsageEventStreamUpdate,
		Endpoint:         "/v1/chat/completions",
		ModelAlias:       "gpt-test",
		Status:           "streaming",
		InternalMetadata: map[string]any{},
		CustomerTags:     map[string]any{},
	}); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}

	events, err := repo.ListEvents(ctx, ListEventsFilter{AccountID: accountID, RequestID: requestID, Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event, got %d", len(events))
	}
	if events[0].LatencyMs != nil {
		t.Fatalf("expected latency_ms nil for an in-flight attempt, got %d", *events[0].LatencyMs)
	}
}

// -----------------------------------------------------------------------------
// GetUsageSummary: current rollup contract. Sums input/output tokens and
// credits correctly. Documents, with a real query, that cache tokens do NOT
// appear on UsageSummaryRow today (issue #1174) — this must go red the
// day someone adds cache columns to the rollup without updating the type,
// or removes them from the type without updating the rollup.
// -----------------------------------------------------------------------------

func TestGetUsageSummary_SumsInputOutputAndCredits(t *testing.T) {
	pool := newUsageTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()
	accountID := seedUsageAccount(t, pool)

	from := time.Now().UTC().Add(-1 * time.Hour)
	// Two DISTINCT attempts, not one attempt written twice: a single
	// request_attempt_id carries at most one 'completed' row
	// (ux_usage_events_completed_attempt, issue #1180's dedup fix), matching
	// production where one attempt has exactly one terminal outcome. This
	// test's subject is SUM() across multiple genuine requests, so it needs
	// multiple genuine attempts to sum over.
	for i := 0; i < 2; i++ {
		attemptID := seedUsageAttempt(t, ctx, repo, accountID)
		if _, err := repo.RecordEvent(ctx, RecordEventInput{
			AccountID:        accountID,
			RequestAttemptID: attemptID,
			RequestID:        "req-" + uuid.NewString(),
			EventType:        UsageEventCompleted,
			Endpoint:         "/v1/chat/completions",
			ModelAlias:       "gpt-summary-test",
			Status:           "completed",
			InputTokens:      100,
			OutputTokens:     50,
			CacheReadTokens:  30,
			CacheWriteTokens: 20,
			HiveCreditDelta:  -1000,
			InternalMetadata: map[string]any{},
			CustomerTags:     map[string]any{},
		}); err != nil {
			t.Fatalf("seed event %d: %v", i, err)
		}
	}
	to := time.Now().UTC().Add(1 * time.Hour)

	rows, err := repo.GetUsageSummary(ctx, AnalyticsFilter{AccountID: accountID, GroupBy: "model", From: from, To: to})
	if err != nil {
		t.Fatalf("GetUsageSummary: %v", err)
	}
	var found *UsageSummaryRow
	for i := range rows {
		if rows[i].GroupKey == "gpt-summary-test" {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatalf("expected a summary row for gpt-summary-test, got %#v", rows)
	}
	if found.TotalInputTokens != 200 {
		t.Fatalf("expected summed input tokens 200 (2x100), got %d", found.TotalInputTokens)
	}
	if found.TotalOutputTokens != 100 {
		t.Fatalf("expected summed output tokens 100 (2x50), got %d", found.TotalOutputTokens)
	}
	if found.TotalCreditsSpent != 2000 {
		t.Fatalf("expected summed abs(credit_delta) 2000 (2x1000), got %d", found.TotalCreditsSpent)
	}
	if found.RequestCount != 2 {
		t.Fatalf("expected request_count 2, got %d", found.RequestCount)
	}

	// Documents the current gap (issue #1174): UsageSummaryRow (types.go) has
	// no CacheReadTokens/CacheWriteTokens fields, so the 60 cache-read and 40
	// cache-write tokens across these two events are structurally invisible
	// to this rollup — there is no column to assert on. A future fix to
	// #1174 that adds those fields to UsageSummaryRow and the SQL above must
	// also extend this test with a cache-token sum assertion; until then this
	// comment is the guard.
}

func TestGetUsageSummary_ExcludesEventsOutsideWindow(t *testing.T) {
	pool := newUsageTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()
	accountID := seedUsageAccount(t, pool)
	attemptID := seedUsageAttempt(t, ctx, repo, accountID)

	if _, err := repo.RecordEvent(ctx, RecordEventInput{
		AccountID:        accountID,
		RequestAttemptID: attemptID,
		RequestID:        "req-" + uuid.NewString(),
		EventType:        UsageEventCompleted,
		Endpoint:         "/v1/chat/completions",
		ModelAlias:       "gpt-window-test",
		Status:           "completed",
		InputTokens:      999,
		OutputTokens:     999,
		HiveCreditDelta:  -1,
		InternalMetadata: map[string]any{},
		CustomerTags:     map[string]any{},
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	// A window that ends before the event was created must exclude it.
	past := time.Now().UTC().Add(-48 * time.Hour)
	rows, err := repo.GetUsageSummary(ctx, AnalyticsFilter{
		AccountID: accountID, GroupBy: "model",
		From: past.Add(-1 * time.Hour), To: past,
	})
	if err != nil {
		t.Fatalf("GetUsageSummary: %v", err)
	}
	for _, row := range rows {
		if row.GroupKey == "gpt-window-test" {
			t.Fatalf("expected the out-of-window event to be excluded, got row %#v", row)
		}
	}
}

// -----------------------------------------------------------------------------
// GetSpendSummary: only negative credit deltas (actual spend) count; a
// positive delta (e.g. a refund/reconciliation credit) must not appear as
// spend.
// -----------------------------------------------------------------------------

func TestGetSpendSummary_OnlyCountsNegativeDeltas(t *testing.T) {
	pool := newUsageTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()
	accountID := seedUsageAccount(t, pool)
	attemptID := seedUsageAttempt(t, ctx, repo, accountID)

	model := "gpt-spend-test-" + uuid.NewString()[:8]
	from := time.Now().UTC().Add(-1 * time.Hour)

	// One real spend (-500) and one positive delta (a refund/credit, +200)
	// that must NOT be counted as spend.
	for _, delta := range []int64{-500, 200} {
		if _, err := repo.RecordEvent(ctx, RecordEventInput{
			AccountID:        accountID,
			RequestAttemptID: attemptID,
			RequestID:        "req-" + uuid.NewString(),
			EventType:        UsageEventCompleted,
			Endpoint:         "/v1/chat/completions",
			ModelAlias:       model,
			Status:           "completed",
			HiveCreditDelta:  delta,
			InternalMetadata: map[string]any{},
			CustomerTags:     map[string]any{},
		}); err != nil {
			t.Fatalf("seed event delta=%d: %v", delta, err)
		}
	}
	to := time.Now().UTC().Add(1 * time.Hour)

	rows, err := repo.GetSpendSummary(ctx, AnalyticsFilter{AccountID: accountID, GroupBy: "model", From: from, To: to})
	if err != nil {
		t.Fatalf("GetSpendSummary: %v", err)
	}
	var found *SpendSummaryRow
	for i := range rows {
		if rows[i].GroupKey == model {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatalf("expected a spend summary row for %s, got %#v", model, rows)
	}
	if found.TotalCredits != 500 {
		t.Fatalf("expected only the -500 delta counted as spend (500 abs), got %d — a positive delta leaked into spend",
			found.TotalCredits)
	}
	if found.EntryCount != 1 {
		t.Fatalf("expected entry_count 1 (only the negative-delta row), got %d", found.EntryCount)
	}
}

// -----------------------------------------------------------------------------
// GetErrorSummary: error rate is computed over ALL requests in the window,
// not just the errored ones, and float division must not divide by zero.
// -----------------------------------------------------------------------------

func TestGetErrorSummary_ComputesRateOverAllRequests(t *testing.T) {
	pool := newUsageTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()
	accountID := seedUsageAccount(t, pool)

	model := "gpt-error-test-" + uuid.NewString()[:8]
	from := time.Now().UTC().Add(-1 * time.Hour)

	// 1 error out of 4 total requests on this model => 25% error rate.
	// Each entry is its own attempt (issue #1180): a single
	// request_attempt_id carries at most one 'completed'-event_type row, so
	// four distinct requests need four distinct attempts, matching how
	// finalizeLocked and edge-api actually write these rows in production.
	statuses := []struct {
		status    string
		errorCode string
	}{
		{"completed", ""},
		{"completed", ""},
		{"completed", ""},
		{"failed", "upstream_5xx"},
	}
	for _, s := range statuses {
		attemptID := seedUsageAttempt(t, ctx, repo, accountID)
		if _, err := repo.RecordEvent(ctx, RecordEventInput{
			AccountID:        accountID,
			RequestAttemptID: attemptID,
			RequestID:        "req-" + uuid.NewString(),
			EventType:        UsageEventCompleted,
			Endpoint:         "/v1/chat/completions",
			ModelAlias:       model,
			Status:           s.status,
			ErrorCode:        s.errorCode,
			InternalMetadata: map[string]any{},
			CustomerTags:     map[string]any{},
		}); err != nil {
			t.Fatalf("seed event status=%s: %v", s.status, err)
		}
	}
	to := time.Now().UTC().Add(1 * time.Hour)

	rows, err := repo.GetErrorSummary(ctx, AnalyticsFilter{AccountID: accountID, GroupBy: "model", From: from, To: to})
	if err != nil {
		t.Fatalf("GetErrorSummary: %v", err)
	}
	var found *ErrorSummaryRow
	for i := range rows {
		if rows[i].GroupKey == model {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatalf("expected an error summary row for %s, got %#v", model, rows)
	}
	if found.TotalRequests != 4 {
		t.Fatalf("expected total_requests 4, got %d", found.TotalRequests)
	}
	if found.ErrorCount != 1 {
		t.Fatalf("expected error_count 1, got %d", found.ErrorCount)
	}
	if found.ErrorRate < 0.249 || found.ErrorRate > 0.251 {
		t.Fatalf("expected error_rate ~0.25 (1/4), got %v", found.ErrorRate)
	}
}

// -----------------------------------------------------------------------------
// UpdateAttemptStatus against real SQL.
// -----------------------------------------------------------------------------

func TestUpdateAttemptStatus_PersistsStatusAndCompletedAt(t *testing.T) {
	pool := newUsageTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()
	accountID := seedUsageAccount(t, pool)
	attemptID := seedUsageAttempt(t, ctx, repo, accountID)

	completedAt := time.Now().UTC()
	if err := repo.UpdateAttemptStatus(ctx, attemptID, "completed", &completedAt); err != nil {
		t.Fatalf("UpdateAttemptStatus: %v", err)
	}

	attempts, err := repo.ListAttempts(ctx, accountID, "", 10)
	if err != nil {
		t.Fatalf("ListAttempts: %v", err)
	}
	var found *RequestAttempt
	for i := range attempts {
		if attempts[i].ID == attemptID {
			found = &attempts[i]
		}
	}
	if found == nil {
		t.Fatalf("expected to find the updated attempt, got %#v", attempts)
	}
	if found.Status != AttemptStatusCompleted {
		t.Fatalf("expected status completed, got %q", found.Status)
	}
	if found.CompletedAt == nil {
		t.Fatal("expected completed_at to be persisted, got nil")
	}
}

// -----------------------------------------------------------------------------
// Nullable grouping key (issue #1347).
//
// usage_events.api_key_id is the only nullable grouping column (endpoint and
// model_alias are NOT NULL), and it goes NULL for two real reasons: the
// request failed before a key was resolved, and the FK is ON DELETE SET NULL,
// so an attributed row loses its key when that key row is deleted. Every
// summary that can group by api_key has to return those rows under an
// explicit unattributed bucket. Scanning group_key into a plain string
// instead failed the entire query with a NULL scan error, which surfaced as
// a 500 on the console overview page.
//
// The fixture below deliberately mixes one attributed row with one
// unattributed row. A fixture of fully attributed rows cannot fail, which is
// how the defect shipped in the first place, and asserting only the
// unattributed bucket would not catch a fix that collapsed every row into it.
// -----------------------------------------------------------------------------

func seedUsageAPIKey(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID) uuid.UUID {
	t.Helper()
	var keyID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO public.api_keys (account_id, nickname, token_hash, redacted_suffix, created_by_user_id)
		 SELECT $1, $2, $3, $4, a.owner_user_id FROM public.accounts a WHERE a.id = $1
		 RETURNING id`,
		accountID, "usage summary test", "usage-summary-test-"+uuid.NewString(), "wxyz",
	).Scan(&keyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	return keyID
}

func TestSummariesByAPIKey_NullKeyGroupsAsUnattributed(t *testing.T) {
	pool := newUsageTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()
	accountID := seedUsageAccount(t, pool)
	keyID := seedUsageAPIKey(t, pool, accountID)

	from := time.Now().UTC().Add(-1 * time.Hour)

	// Attributed: an ordinary successful request charged to a real key.
	attributedAttempt := seedUsageAttempt(t, ctx, repo, accountID)
	if _, err := repo.RecordEvent(ctx, RecordEventInput{
		AccountID:        accountID,
		RequestAttemptID: attributedAttempt,
		APIKeyID:         &keyID,
		RequestID:        "req-" + uuid.NewString(),
		EventType:        UsageEventCompleted,
		Endpoint:         "/v1/chat/completions",
		ModelAlias:       "gpt-null-key-test",
		Status:           "completed",
		InputTokens:      100,
		OutputTokens:     50,
		HiveCreditDelta:  -1000,
		InternalMetadata: map[string]any{},
		CustomerTags:     map[string]any{},
	}); err != nil {
		t.Fatalf("seed attributed event: %v", err)
	}

	// Unattributed: an error recorded before a key was resolved. This is the
	// exact row shape that used to fail the whole summary query.
	unattributedAttempt := seedUsageAttempt(t, ctx, repo, accountID)
	if _, err := repo.RecordEvent(ctx, RecordEventInput{
		AccountID:        accountID,
		RequestAttemptID: unattributedAttempt,
		APIKeyID:         nil,
		RequestID:        "req-" + uuid.NewString(),
		EventType:        UsageEventCompleted,
		Endpoint:         "/v1/chat/completions",
		ModelAlias:       "gpt-null-key-test",
		Status:           "failed",
		ErrorCode:        "unauthorized",
		ErrorType:        "authentication_error",
		InputTokens:      7,
		OutputTokens:     3,
		HiveCreditDelta:  -500,
		InternalMetadata: map[string]any{},
		CustomerTags:     map[string]any{},
	}); err != nil {
		t.Fatalf("seed unattributed event: %v", err)
	}

	// Guard the fixture itself: if that row is not actually NULL, every
	// assertion below would pass against a defect that is still present.
	var nullRows int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM public.usage_events WHERE account_id = $1 AND api_key_id IS NULL`,
		accountID,
	).Scan(&nullRows); err != nil {
		t.Fatalf("count null api_key_id rows: %v", err)
	}
	if nullRows != 1 {
		t.Fatalf("fixture must contain exactly one NULL api_key_id row, got %d", nullRows)
	}

	to := time.Now().UTC().Add(1 * time.Hour)
	filter := AnalyticsFilter{AccountID: accountID, GroupBy: "api_key", From: from, To: to}

	t.Run("error summary", func(t *testing.T) {
		rows, err := repo.GetErrorSummary(ctx, filter)
		if err != nil {
			t.Fatalf("GetErrorSummary must tolerate a NULL group key: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("expected 2 groups (one key, one unattributed), got %d: %#v", len(rows), rows)
		}
		byKey := make(map[string]ErrorSummaryRow, len(rows))
		for _, row := range rows {
			byKey[row.GroupKey] = row
		}
		attributed, ok := byKey[keyID.String()]
		if !ok {
			t.Fatalf("expected a group keyed by the real api key %s, got %#v", keyID, rows)
		}
		if attributed.ErrorCount != 0 || attributed.TotalRequests != 1 {
			t.Fatalf("attributed group: expected 0 errors of 1 request, got %d of %d",
				attributed.ErrorCount, attributed.TotalRequests)
		}
		unattributed, ok := byKey[UnattributedGroupKey]
		if !ok {
			t.Fatalf("expected the NULL key to group as %q, got %#v", UnattributedGroupKey, rows)
		}
		if unattributed.ErrorCount != 1 || unattributed.TotalRequests != 1 {
			t.Fatalf("unattributed group: expected 1 error of 1 request, got %d of %d",
				unattributed.ErrorCount, unattributed.TotalRequests)
		}
		if unattributed.ErrorRate < 0.999 || unattributed.ErrorRate > 1.001 {
			t.Fatalf("unattributed group: expected error_rate 1.0, got %v", unattributed.ErrorRate)
		}
	})

	t.Run("usage summary", func(t *testing.T) {
		rows, err := repo.GetUsageSummary(ctx, filter)
		if err != nil {
			t.Fatalf("GetUsageSummary must tolerate a NULL group key: %v", err)
		}
		byKey := make(map[string]UsageSummaryRow, len(rows))
		for _, row := range rows {
			byKey[row.GroupKey] = row
		}
		if _, ok := byKey[keyID.String()]; !ok {
			t.Fatalf("expected a group keyed by the real api key %s, got %#v", keyID, rows)
		}
		unattributed, ok := byKey[UnattributedGroupKey]
		if !ok {
			t.Fatalf("expected the NULL key to group as %q, got %#v", UnattributedGroupKey, rows)
		}
		if unattributed.RequestCount != 1 || unattributed.TotalInputTokens != 7 || unattributed.TotalOutputTokens != 3 {
			t.Fatalf("unattributed group: expected 1 request with 7 input and 3 output tokens, got %d with %d and %d",
				unattributed.RequestCount, unattributed.TotalInputTokens, unattributed.TotalOutputTokens)
		}
	})

	t.Run("spend summary", func(t *testing.T) {
		rows, err := repo.GetSpendSummary(ctx, filter)
		if err != nil {
			t.Fatalf("GetSpendSummary must tolerate a NULL group key: %v", err)
		}
		byKey := make(map[string]SpendSummaryRow, len(rows))
		for _, row := range rows {
			byKey[row.GroupKey] = row
		}
		attributed, ok := byKey[keyID.String()]
		if !ok {
			t.Fatalf("expected a group keyed by the real api key %s, got %#v", keyID, rows)
		}
		if attributed.TotalCredits != 1000 {
			t.Fatalf("attributed group: expected 1000 credits, got %d", attributed.TotalCredits)
		}
		unattributed, ok := byKey[UnattributedGroupKey]
		if !ok {
			t.Fatalf("expected the NULL key to group as %q, got %#v", UnattributedGroupKey, rows)
		}
		if unattributed.TotalCredits != 500 || unattributed.EntryCount != 1 {
			t.Fatalf("unattributed group: expected 500 credits over 1 entry, got %d over %d",
				unattributed.TotalCredits, unattributed.EntryCount)
		}
	})
}
