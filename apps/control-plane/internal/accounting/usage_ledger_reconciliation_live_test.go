package accounting

import (
	"context"
	"testing"

	"github.com/google/uuid"
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
