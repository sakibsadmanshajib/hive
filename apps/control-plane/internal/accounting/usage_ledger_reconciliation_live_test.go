package accounting

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usage"
)

// TestUsageEventsAgreeWithLedgerAfterDuplicateCompletedWrite is issue #1180's
// guard. usage_events.hive_credit_delta and credit_ledger_entries.
// credits_delta disagreed for the same request: control-plane's own
// finalizeLocked writes the authoritative 'completed' row (hive_credit_delta
// = -actualCredits, the same figure charged to the ledger), and edge-api
// separately POSTs a SECOND 'completed' row for the identical attempt with
// hive_credit_delta set to a raw token count -- unrelated to money, wrong on
// every request that hits it. This test reproduces both writes against a
// real database through the real repositories, the way the live pair (an
// attempt's two 'completed' rows, one -385, one +151) was measured on the
// demo box on 2026-08-25.
//
// Regressing to a plain INSERT (dropping the ON CONFLICT in
// usage/repository.go's RecordEvent, or dropping the partial unique index
// ux_usage_events_completed_attempt) reintroduces the duplicate row and this
// test fails on the row-count assertion. Regressing the DO UPDATE to also
// overwrite hive_credit_delta reintroduces the money-figure defect and this
// test fails on the credit-delta assertion instead.
func TestUsageEventsAgreeWithLedgerAfterDuplicateCompletedWrite(t *testing.T) {
	pool := newAccountingTestPool(t)
	accountID := seedReleaseIdempotencyAccount(t, pool)
	ctx := context.Background()

	ledgerSvc := ledger.NewService(ledger.NewPgxRepository(pool))
	usageSvc := usage.NewService(usage.NewPgxRepository(pool))
	svc := NewService(NewPgxRepository(pool), ledgerSvc, usageSvc)

	if _, err := ledgerSvc.GrantCredits(ctx, accountID, uuid.NewString(), 300000, map[string]any{"reason": "test grant"}); err != nil {
		t.Fatalf("grant credits: %v", err)
	}

	const (
		hold           = int64(10000)
		actualCredits  = int64(385)
		measuredInput  = int64(73)
		measuredOutput = int64(78)
	)

	reservation, err := svc.CreateReservation(ctx, CreateReservationInput{
		AccountID:        accountID,
		RequestID:        uuid.NewString(),
		AttemptNumber:    1,
		Endpoint:         "/v1/chat/completions",
		ModelAlias:       "hive-fast",
		EstimatedCredits: hold,
	})
	if err != nil {
		t.Fatalf("CreateReservation: %v", err)
	}

	// Write 1: control-plane's own finalizeLocked, the authoritative write.
	// Mirrors production exactly: orchestrator.go and stream.go never
	// populate FinalizeReservationInput.InputTokens/OutputTokens (a separate,
	// edge-api-side gap, out of this fix's reach), so this row lands with
	// zero token counts and the correct, ledger-matching credit delta.
	if _, err := svc.FinalizeReservation(ctx, FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservation.ID,
		ActualCredits:          actualCredits,
		TerminalUsageConfirmed: true,
		Status:                 string(usage.AttemptStatusCompleted),
	}); err != nil {
		t.Fatalf("FinalizeReservation: %v", err)
	}

	// Write 2: edge-api's separate, unconditional POST to
	// /internal/usage/events (recordCompletedEvent), reproduced directly
	// against usage.Service.RecordEvent the way usage/http.go's handler
	// calls it. hive_credit_delta here is TotalTokens, exactly the wrong
	// value edge-api sends today; input/output tokens are the real measured
	// counts finalizeLocked never received.
	wrongDelta := measuredInput + measuredOutput
	if _, err := usageSvc.RecordEvent(ctx, usage.RecordEventInput{
		AccountID:        accountID,
		RequestAttemptID: reservation.RequestAttemptID,
		RequestID:        reservation.RequestID,
		EventType:        usage.UsageEventCompleted,
		Endpoint:         reservation.Endpoint,
		ModelAlias:       reservation.ModelAlias,
		Status:           "completed",
		InputTokens:      measuredInput,
		OutputTokens:     measuredOutput,
		HiveCreditDelta:  wrongDelta,
	}); err != nil {
		t.Fatalf("second (edge-api-shaped) RecordEvent: %v", err)
	}

	// Exactly one 'completed' row: the duplicate must fold, not insert.
	var rowCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.usage_events WHERE account_id = $1 AND request_attempt_id = $2 AND event_type = 'completed'`,
		accountID, reservation.RequestAttemptID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count usage_events: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("got %d 'completed' usage_events rows for one attempt, want 1 (the duplicate-write defect from issue #1180 reintroduced)", rowCount)
	}

	// The surviving row's hive_credit_delta must still agree with the
	// ledger, not the second write's wrong value.
	var usageDelta, ledgerDelta, inputTokens, outputTokens int64
	if err := pool.QueryRow(ctx,
		`SELECT hive_credit_delta, input_tokens, output_tokens FROM public.usage_events
		  WHERE account_id = $1 AND request_attempt_id = $2 AND event_type = 'completed'`,
		accountID, reservation.RequestAttemptID,
	).Scan(&usageDelta, &inputTokens, &outputTokens); err != nil {
		t.Fatalf("read surviving usage_events row: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT credits_delta FROM public.credit_ledger_entries WHERE account_id = $1 AND attempt_id = $2 AND entry_type = 'usage_charge'`,
		accountID, reservation.RequestAttemptID,
	).Scan(&ledgerDelta); err != nil {
		t.Fatalf("read ledger charge entry: %v", err)
	}
	if usageDelta != ledgerDelta {
		t.Fatalf("usage_events.hive_credit_delta = %d, credit_ledger_entries.credits_delta = %d: the two money surfaces disagree for the same request (issue #1180)", usageDelta, ledgerDelta)
	}
	if usageDelta != -actualCredits {
		t.Fatalf("usage_events.hive_credit_delta = %d, want %d (the second write's wrong value, %d, must never win)", usageDelta, -actualCredits, wrongDelta)
	}

	// The token counts from the second write must still land: dedup should
	// not silently drop the only real measurement edge-api sent.
	if inputTokens != measuredInput || outputTokens != measuredOutput {
		t.Fatalf("merged token counts = (%d, %d), want (%d, %d): the duplicate write's real measurement was dropped instead of folded in", inputTokens, outputTokens, measuredInput, measuredOutput)
	}
}

// TestMigrationSurvivesOrphanedRequestAttempt is the post-merge regression
// guard for the 2026-08-25 deploy failure (deploy-demo-box run 32912034013):
// this migration's Step 2 (the ledger-reconciliation UPDATE) errored with
// "insert or update on table usage_events violates foreign key constraint
// usage_events_request_attempt_id_fkey" because the live database already
// held usage_events rows whose request_attempt_id no longer has a
// request_attempts parent (issue #1102: a retention purge deletes
// request_attempts without going through the ON DELETE CASCADE trigger).
// The throwaway database this migration was validated against before
// merging had no such rows, so the defect never showed up until it hit real
// data. This test fabricates that exact precondition -- a 'completed'
// usage_events row in the stale pre-rescale unit, orphaned from its
// request_attempts parent -- and then executes the actual, on-disk
// migration file verbatim, the same way scripts/apply-migrations.sh does.
// Dropping the EXISTS guard this migration's Step 2 now carries reproduces
// the FK error and fails this test at the Exec call; reconciling an
// orphaned row anyway (rather than skipping it) fails the assertion below
// instead.
func TestMigrationSurvivesOrphanedRequestAttempt(t *testing.T) {
	pool := newAccountingTestPool(t)
	accountID := seedReleaseIdempotencyAccount(t, pool)
	ctx := context.Background()

	ledgerSvc := ledger.NewService(ledger.NewPgxRepository(pool))
	usageSvc := usage.NewService(usage.NewPgxRepository(pool))
	svc := NewService(NewPgxRepository(pool), ledgerSvc, usageSvc)

	if _, err := ledgerSvc.GrantCredits(ctx, accountID, uuid.NewString(), 10000000, map[string]any{"reason": "test grant"}); err != nil {
		t.Fatalf("grant credits: %v", err)
	}

	const (
		hold          = int64(5000000)
		actualCredits = int64(2500000)
		staleDelta    = actualCredits / 10000 // the pre-rescale, wrong-unit value Step 2 exists to fix
	)

	reservation, err := svc.CreateReservation(ctx, CreateReservationInput{
		AccountID:        accountID,
		RequestID:        uuid.NewString(),
		AttemptNumber:    1,
		Endpoint:         "/v1/chat/completions",
		ModelAlias:       "hive-fast",
		EstimatedCredits: hold,
	})
	if err != nil {
		t.Fatalf("CreateReservation: %v", err)
	}

	if _, err := svc.FinalizeReservation(ctx, FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservation.ID,
		ActualCredits:          actualCredits,
		TerminalUsageConfirmed: true,
		Status:                 string(usage.AttemptStatusCompleted),
	}); err != nil {
		t.Fatalf("FinalizeReservation: %v", err)
	}

	// Capture the authoritative (earliest) row's id before the second,
	// edge-api-shaped write lands, so the stale-unit UPDATE below can target
	// it specifically once two 'completed' rows exist for this attempt.
	var keeperID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM public.usage_events WHERE account_id = $1 AND request_attempt_id = $2 AND event_type = 'completed'`,
		accountID, reservation.RequestAttemptID,
	).Scan(&keeperID); err != nil {
		t.Fatalf("read authoritative usage_events row id: %v", err)
	}

	// Write 2: edge-api's redundant duplicate 'completed' write (issue
	// #1180's own precondition, see the sibling test above). This is the
	// live shape that actually reproduced the FK crash on the demo box:
	// with two 'completed' rows for one attempt, Step 1 folds this second
	// row into the keeper (an UPDATE, then a DELETE) immediately before
	// Step 2 touches that same keeper row again. A fixture with only one
	// 'completed' row never exercises that sequence and would pass even
	// against the unpatched migration.
	if _, err := usageSvc.RecordEvent(ctx, usage.RecordEventInput{
		AccountID:        accountID,
		RequestAttemptID: reservation.RequestAttemptID,
		RequestID:        reservation.RequestID,
		EventType:        usage.UsageEventCompleted,
		Endpoint:         reservation.Endpoint,
		ModelAlias:       reservation.ModelAlias,
		Status:           "completed",
		InputTokens:      1,
		OutputTokens:     1,
		HiveCreditDelta:  2,
	}); err != nil {
		t.Fatalf("second (edge-api-shaped) RecordEvent: %v", err)
	}

	// Roll the authoritative row back to the stale pre-rescale unit
	// finalizeLocked would have written before 20260823_40, so Step 2's
	// reconciliation predicate has something to match.
	if _, err := pool.Exec(ctx,
		`UPDATE public.usage_events SET hive_credit_delta = $1 WHERE id = $2`,
		-staleDelta, keeperID,
	); err != nil {
		t.Fatalf("set stale-unit hive_credit_delta: %v", err)
	}

	orphanRequestAttempt(t, pool, reservation.RequestAttemptID)

	migrationSQL := readRepoFileForAccounting(t, "supabase/migrations/20260825_03_usage_events_completed_dedup_and_rescale_backfill.sql")
	if !strings.Contains(migrationSQL, "SELECT 1 FROM public.request_attempts ra WHERE ra.id = ue.request_attempt_id") {
		t.Fatal("migration file no longer guards Step 2 against orphaned request_attempt_id rows (issue #1102); the guard was removed")
	}
	// The migration file is a multi-statement script (BEGIN ... COMMIT), the
	// same shape scripts/apply-migrations.sh hands to psql -f. pgx's default
	// extended protocol rejects multiple commands in one Exec call, so this
	// goes through PgConn.Exec directly, which uses the simple query
	// protocol -- the same protocol psql uses -- for parity with how this
	// file actually runs in production.
	execConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection to run migration: %v", err)
	}
	_, err = execConn.Conn().PgConn().Exec(ctx, migrationSQL).ReadAll()
	execConn.Release()
	if err != nil {
		t.Fatalf("migration failed against a database containing an orphaned usage_events row: %v", err)
	}

	var delta int64
	var metadata []byte
	if err := pool.QueryRow(ctx,
		`SELECT hive_credit_delta, internal_metadata FROM public.usage_events
		  WHERE account_id = $1 AND request_attempt_id = $2 AND event_type = 'completed'`,
		accountID, reservation.RequestAttemptID,
	).Scan(&delta, &metadata); err != nil {
		t.Fatalf("read orphaned usage_events row after migration: %v", err)
	}
	if delta != -staleDelta {
		t.Fatalf("orphaned row's hive_credit_delta = %d, want unchanged %d: Step 2 must skip a row with no live request_attempts parent, not reconcile it", delta, -staleDelta)
	}
	if strings.Contains(string(metadata), "ledger_reconciled_from") {
		t.Fatal("orphaned row was reconciled (internal_metadata carries ledger_reconciled_from); Step 2 must leave orphaned rows untouched")
	}
}

// orphanRequestAttempt deletes attemptID's request_attempts row while
// bypassing the ON DELETE CASCADE trigger, reproducing the exact live shape
// issue #1102 describes: the FK is CASCADE, but the retention purge that
// creates these orphans does not go through it. session_replication_role is
// scoped to one dedicated connection, set and reset around the single
// DELETE, so no other concurrent test on the shared pool ever observes
// triggers disabled.
func orphanRequestAttempt(t *testing.T, pool *pgxpool.Pool, attemptID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire dedicated connection: %v", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SET session_replication_role = replica"); err != nil {
		t.Fatalf("disable triggers for orphan fixture: %v", err)
	}
	defer func() {
		if _, err := conn.Exec(ctx, "SET session_replication_role = DEFAULT"); err != nil {
			t.Errorf("re-enable triggers after orphan fixture: %v", err)
		}
	}()

	tag, err := conn.Exec(ctx, "DELETE FROM public.request_attempts WHERE id = $1", attemptID)
	if err != nil {
		t.Fatalf("delete request_attempts row without cascade: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected to delete exactly 1 request_attempts row, deleted %d", tag.RowsAffected())
	}
}

// readRepoFileForAccounting reads relativePath from the repository root,
// located by walking up from the test's working directory to the nearest
// go.work file. Mirrors internal/routing/repository_schema_test.go's
// repoRoot/readRepoFile pair; not shared across packages because none of
// this repo's Go test helpers are exported.
func readRepoFileForAccounting(t *testing.T, relativePath string) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	root := wd
	for {
		if _, err := os.Stat(filepath.Join(root, "go.work")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatalf("could not find repository root from %s", wd)
		}
		root = parent
	}

	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relativePath)))
	if err != nil {
		t.Fatalf("read %s: %v", relativePath, err)
	}
	return string(body)
}
