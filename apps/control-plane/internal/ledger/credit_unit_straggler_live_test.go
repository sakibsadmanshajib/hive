package ledger

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// =============================================================================
// The straggler detector, issue #1704.
//
// Migration 20260823_40_credit_unit_rescale_billion.sql documented a detector
// for rows that missed the rescale, and its predicate was "nonzero and carries
// no credit_unit key". On the live box that query returns seven rows, every
// one of them a correctly scaled grant seeded by SQL rather than through
// PostEntry, and the remedy documented next to it (multiply the matches by
// 10,000) would have inflated them by four orders of magnitude.
//
// A MISSING STAMP IS NOT EVIDENCE OF AN OLD UNIT. It is evidence of a writer
// that did not stamp. What separates the two populations is WHEN the row was
// written, against the boundary the database itself records in
// public.credit_unit_rescale, and the comparison runs in OPPOSITE directions
// on the two tables the old runbook queried:
//
//   - credit_ledger_entries: the rescale stamped every row it scaled, so an
//     unstamped row from BEFORE the boundary is one the scan missed, and an
//     unstamped row from after it is simply a writer that does not stamp.
//   - payment_intents: the rescale scaled every row and stamped NONE of them
//     (its step 6 has no metadata clause at all), so the older rows are the
//     correctly scaled ones and a candidate sits in a band around the boundary:
//     an hour on either side, covering a row inserted while that transaction
//     ran (outside its snapshot, never scaled) and a row an old binary wrote
//     during the recreate that followed.
//
// public.credit_unit_straggler_candidates is the corrected detector, and these
// suites hold both populations side by side in one database so that a view
// which cannot tell them apart fails here rather than on a live balance.
//
// Gated on HIVE_TEST_DB_URL like every other live suite in this package.
// =============================================================================

// rescaleBoundary reads the boundary out of the database rather than assuming
// a date, exactly as invoices.CreditRescaleAppliedAt does on the repair side.
// Everything these tests seed is positioned relative to what Postgres reports.
func rescaleBoundary(t *testing.T, pool *pgxpool.Pool) time.Time {
	t.Helper()
	var at time.Time
	err := pool.QueryRow(context.Background(),
		`SELECT applied_at FROM public.credit_unit_rescale WHERE id = 1`).Scan(&at)
	if err != nil {
		// Fatal, not Skip: the CI leg that runs this suite applies the whole
		// migration chain, so a missing marker is a schema regression and
		// skipping would report it green.
		t.Fatalf("read the rescale boundary (is this a migrated test DB?): %v", err)
	}
	return at
}

// insertLedgerEntry writes straight through SQL because that is the whole
// point: PostEntry stamps, and the rows this defect is about were written by
// something that does not.
func insertLedgerEntry(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, delta int64, createdAt time.Time, metadata map[string]any) uuid.UUID {
	t.Helper()
	raw, err := json.Marshal(normalizeMetadata(metadata))
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO public.credit_ledger_entries
			(account_id, entry_type, credits_delta, idempotency_key, metadata, created_at)
		VALUES ($1, 'grant', $2, $3, $4::jsonb, $5)
		RETURNING id
	`, accountID, delta, "straggler-"+uuid.NewString(), string(raw), createdAt).Scan(&id); err != nil {
		t.Fatalf("seed ledger entry: %v", err)
	}
	return id
}

func insertPaymentIntent(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, credits int64, createdAt time.Time, metadata map[string]any) uuid.UUID {
	t.Helper()
	raw, err := json.Marshal(normalizeMetadata(metadata))
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO public.payment_intents
			(account_id, rail, status, credits, amount_usd, idempotency_key, metadata, created_at)
		VALUES ($1, 'stripe', 'completed', $2, 0, $3, $4::jsonb, $5)
		RETURNING id
	`, accountID, credits, "straggler-"+uuid.NewString(), string(raw), createdAt).Scan(&id); err != nil {
		t.Fatalf("seed payment intent: %v", err)
	}
	return id
}

// insertReservationEvent seeds the FK chain a reservation event needs
// (request attempt, reservation) so the view's join to credit_reservations for
// an account_id is exercised against real SQL rather than assumed.
func insertReservationEvent(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, delta int64, createdAt time.Time, metadata map[string]any) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	raw, err := json.Marshal(normalizeMetadata(metadata))
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	var attemptID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO public.request_attempts
			(account_id, request_id, attempt_number, endpoint, model_alias, status)
		VALUES ($1, $2, 1, '/v1/chat/completions', 'hive-default', 'completed')
		RETURNING id
	`, accountID, "straggler-"+uuid.NewString()).Scan(&attemptID); err != nil {
		t.Fatalf("seed request attempt: %v", err)
	}

	var reservationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO public.credit_reservations
			(account_id, request_attempt_id, reservation_key, status, reserved_credits)
		VALUES ($1, $2, $3, 'finalized', 1000)
		RETURNING id
	`, accountID, attemptID, "straggler-"+uuid.NewString()).Scan(&reservationID); err != nil {
		t.Fatalf("seed reservation: %v", err)
	}

	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO public.credit_reservation_events
			(reservation_id, event_type, credits_delta, reason, metadata, created_at)
		VALUES ($1, 'reserved', $2, 'straggler-detector-test', $3::jsonb, $4)
		RETURNING id
	`, reservationID, delta, string(raw), createdAt).Scan(&id); err != nil {
		t.Fatalf("seed reservation event: %v", err)
	}
	return id
}

type stragglerCandidate struct {
	SourceTable string
	RowID       uuid.UUID
	Credits     int64
}

// candidates reads the view for ONE account, so a shared CI database carrying
// other suites' rows cannot make this assertion pass or fail by accident.
func candidates(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID) []stragglerCandidate {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT source_table, row_id, credits
		  FROM public.credit_unit_straggler_candidates
		 WHERE account_id = $1
		 ORDER BY created_at, source_table
	`, accountID)
	if err != nil {
		t.Fatalf("query public.credit_unit_straggler_candidates: %v", err)
	}
	defer rows.Close()

	var out []stragglerCandidate
	for rows.Next() {
		var c stragglerCandidate
		if err := rows.Scan(&c.SourceTable, &c.RowID, &c.Credits); err != nil {
			t.Fatalf("scan candidate: %v", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate candidates: %v", err)
	}
	return out
}

func requireOnlyCandidate(t *testing.T, got []stragglerCandidate, wantTable string, wantID uuid.UUID) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("detector selected %d rows, want exactly the one genuine candidate: %+v", len(got), got)
	}
	if got[0].SourceTable != wantTable || got[0].RowID != wantID {
		t.Fatalf("detector selected %s/%s, want %s/%s", got[0].SourceTable, got[0].RowID, wantTable, wantID)
	}
}

// TestStragglerDetectorTellsAnUnstampedRowFromAnUnscaledOne_Live is the
// distinction the old query could not make: two unstamped ledger rows, one on
// each side of the boundary, and only the pre-boundary one is a candidate.
func TestStragglerDetectorTellsAnUnstampedRowFromAnUnscaledOne_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	boundary := rescaleBoundary(t, pool)
	accountID := seedLedgerAccount(t, pool)

	// Genuinely pre-boundary and unstamped: the rescale stamped everything it
	// scaled, so this row is one the scan missed. Old-unit magnitude.
	unscaled := insertLedgerEntry(t, pool, accountID, 84_276, boundary.Add(-24*time.Hour), nil)

	// The live population this issue is about: written a day AFTER the rescale
	// by seed SQL, correctly scaled, unstamped only because it bypassed
	// PostEntry. Multiplying this by 10,000 is the outcome #1704 exists to
	// prevent.
	insertLedgerEntry(t, pool, accountID, 100_000_000_000, boundary.Add(24*time.Hour), nil)

	// Stamped by the current binary, and a zero-delta row: zero means the same
	// thing in both units, so it is not evidence of anything.
	insertLedgerEntry(t, pool, accountID, 5_000_000, boundary.Add(48*time.Hour), map[string]any{"credit_unit": CreditUnitV2})
	insertLedgerEntry(t, pool, accountID, 0, boundary.Add(-48*time.Hour), nil)

	requireOnlyCandidate(t, candidates(t, pool, accountID), "credit_ledger_entries", unscaled)
}

// TestStragglerDetectorInvertsTheBoundaryForPaymentIntents_Live pins the half
// of this that is easiest to get backwards. The rescale scaled every intent
// and stamped none of them, so on THIS table a pre-boundary unstamped row is
// the correctly scaled one. Five such rows exist on the live box today, and
// the old remedy would have multiplied all five.
func TestStragglerDetectorInvertsTheBoundaryForPaymentIntents_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	boundary := rescaleBoundary(t, pool)
	accountID := seedLedgerAccount(t, pool)

	// Scaled by migration 20260823_40 step 6, which stamps nothing. Correct
	// today, and NOT a candidate.
	insertPaymentIntent(t, pool, accountID, 9_990_000_000, boundary.Add(-30*24*time.Hour), nil)

	// Inserted by another session while the rescale transaction was running:
	// outside its snapshot, so step 6 never scaled it, and it carries a
	// created_at just UNDER the marker. Pre-boundary and still a candidate,
	// which is why this arm is a band rather than "everything after".
	raced := insertPaymentIntent(t, pool, accountID, 84_276, boundary.Add(-30*time.Minute), nil)

	// Written by an old binary during the container recreate window: after the
	// boundary, unstamped, and therefore genuinely ambiguous.
	straggler := insertPaymentIntent(t, pool, accountID, 999_000, boundary.Add(2*time.Minute), nil)

	// The current binary stamps at creation (payments.Service.InitiateCheckout).
	insertPaymentIntent(t, pool, accountID, 10_000_000_000, boundary.Add(72*time.Hour), map[string]any{"credit_unit": CreditUnitV2})

	// Unstamped, but written days after the recreate window closed: a writer
	// that does not stamp, exactly like the seven ledger grants, and not a
	// candidate. Without the arm's upper bound this row would be a permanent
	// false positive and the detector would stop meaning zero.
	insertPaymentIntent(t, pool, accountID, 250_000_000, boundary.Add(72*time.Hour), nil)

	got := candidates(t, pool, accountID)
	if len(got) != 2 {
		t.Fatalf("detector selected %d rows, want the raced insert and the recreate-window straggler: %+v", len(got), got)
	}
	selected := map[uuid.UUID]bool{got[0].RowID: true, got[1].RowID: true}
	if !selected[raced] || !selected[straggler] {
		t.Fatalf("detector selected %+v, want %s (raced insert) and %s (recreate window)", got, raced, straggler)
	}
	for _, c := range got {
		if c.SourceTable != "payment_intents" {
			t.Fatalf("unexpected source table %q in %+v", c.SourceTable, got)
		}
	}
}

// TestStragglerDetectorWithoutABoundaryOpensUp_Live holds the failure mode
// this detector must not have. Every predicate in the view is anchored to a
// boundary read out of the database, so the interesting question is what it
// does when that boundary cannot be read: the marker table is row level
// secured with no policies, and a connecting role without BYPASSRLS sees it
// empty. A plain scalar subquery would have returned NO rows there, and an
// empty result reads as "clean" when it actually means "this database cannot
// answer the question": a pending answer taken for a negative one. The
// aggregate in the view yields NULL instead, and NULL opens every arm, so an
// operator gets a pile to reconcile rather than a false all-clear.
//
// The marker is removed inside a transaction that is always rolled back;
// nothing is committed. The rescale migration's own header is emphatic that
// this row must never actually be deleted, because with it gone a replay
// would double every balance.
func TestStragglerDetectorWithoutABoundaryOpensUp_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	ctx := context.Background()
	boundary := rescaleBoundary(t, pool)
	accountID := seedLedgerAccount(t, pool)

	// A row no boundary-aware arm would ever select: stamped by nobody, well
	// clear of the recreate window.
	insertLedgerEntry(t, pool, accountID, 100_000_000_000, boundary.Add(96*time.Hour), nil)
	if got := candidates(t, pool, accountID); len(got) != 0 {
		t.Fatalf("with a readable boundary this row is not a candidate, got %+v", got)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("the marker deletion MUST NOT survive this test: rollback failed: %v", err)
		}
	}()

	if _, err := tx.Exec(ctx, `DELETE FROM public.credit_unit_rescale WHERE id = 1`); err != nil {
		t.Fatalf("hide the boundary: %v", err)
	}

	var n int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM public.credit_unit_straggler_candidates WHERE account_id = $1
	`, accountID).Scan(&n); err != nil {
		t.Fatalf("query candidates without a boundary: %v", err)
	}
	if n == 0 {
		t.Fatal("with no boundary the detector reported a clean result; an unanswerable question must not read as a negative answer")
	}
}

// TestStragglerDetectorCoversReservationEventsOnTheLedgerRule_Live: the rescale
// DID stamp the reservation events it scaled, so this table follows the ledger
// rule, not the intents rule. The live box carries thousands of post-boundary
// unstamped events because no writer on that path stamps; none of them is a
// straggler and the detector must not say otherwise.
func TestStragglerDetectorCoversReservationEventsOnTheLedgerRule_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	boundary := rescaleBoundary(t, pool)
	accountID := seedLedgerAccount(t, pool)

	missed := insertReservationEvent(t, pool, accountID, -5_000, boundary.Add(-72*time.Hour), nil)
	insertReservationEvent(t, pool, accountID, -50_000_000, boundary.Add(96*time.Hour), nil)

	requireOnlyCandidate(t, candidates(t, pool, accountID), "credit_reservation_events", missed)
}
