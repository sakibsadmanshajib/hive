package accounting

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usage"
)

// The reaper's own regression suite (reaper_test.go) proves RunOnce's control
// flow against a hand-written reaperLedger and an in-memory staleReservationLister
// stub: neither ever executes ListStaleReservations' real SQL. That query is the
// entire reason the reaper exists (issue #616): a hold the money path failed to
// release is only ever found by it, and it is the one piece of this package
// that has never run against a real Postgres before this file. A wrong column,
// an inverted cutoff comparison, or a type mismatch between
// credit_reservations.id and batches.reservation_id (uuid vs text, cast with
// ::text in the query) would pass every mocked test in this package and still
// leave every stranded hold on the demo box un-found.
//
// Gated on HIVE_TEST_DB_URL, same convention as the package's other _live tests.

func TestReaperRunOnce_Live(t *testing.T) {
	pool := newAccountingTestPool(t)
	ctx := context.Background()

	accountID := seedReleaseIdempotencyAccount(t, pool)
	ledgerSvc := ledger.NewService(ledger.NewPgxRepository(pool))
	usageSvc := usage.NewService(usage.NewPgxRepository(pool))
	repo := NewPgxRepository(pool)
	svc := NewService(repo, ledgerSvc, usageSvc)

	if _, err := ledgerSvc.GrantCredits(ctx, accountID, uuid.NewString(), 100000, map[string]any{"reason": "reaper live test grant"}); err != nil {
		t.Fatalf("grant credits: %v", err)
	}

	// Genuinely stale: a hold nothing ever settled, backdated past the TTL.
	stale := createTestReservation(t, ctx, svc, accountID, "/v1/chat/completions", "hive-fast", 4000)
	backdateReservation(t, ctx, pool, stale.ID, 2*time.Hour)

	// Positive control: a reservation created moments ago must not be treated
	// as abandoned. Without this, a reaper that released EVERY active/expanded
	// row regardless of age would still pass the assertion on `stale` alone.
	fresh := createTestReservation(t, ctx, svc, accountID, "/v1/chat/completions", "hive-fast", 1500)

	// Stale AND attached to a batch still in flight: ListStaleReservations must
	// exclude it structurally (a batch legitimately holds credits for its whole
	// 24h completion window), never by the age check alone.
	inFlightBatch := createTestReservation(t, ctx, svc, accountID, "/v1/batches", "hive-fast", 9000)
	backdateReservation(t, ctx, pool, inFlightBatch.ID, 2*time.Hour)
	seedBatchRow(t, ctx, pool, accountID, inFlightBatch.ID, "in_progress")

	// Stale AND attached to a batch that has already reached a terminal status:
	// the batch link must not exclude it forever, only while the batch is
	// genuinely running. This is the case that would silently strand a hold
	// permanently if the NOT IN status list in the query were wrong (e.g. it
	// listed no terminal statuses at all, or listed the wrong ones).
	doneBatch := createTestReservation(t, ctx, svc, accountID, "/v1/batches", "hive-fast", 2500)
	backdateReservation(t, ctx, pool, doneBatch.ID, 2*time.Hour)
	seedBatchRow(t, ctx, pool, accountID, doneBatch.ID, "completed")

	reaper := NewReaper(repo, svc, ReaperConfig{TTL: time.Hour})
	result, err := reaper.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	released := map[uuid.UUID]bool{}
	for _, r := range []Reservation{stale, inFlightBatch, doneBatch, fresh} {
		row, err := repo.GetReservation(ctx, accountID, r.ID)
		if err != nil {
			t.Fatalf("reload reservation %s: %v", r.ID, err)
		}
		released[r.ID] = row.Status == ReservationStatusReleased
	}

	if !released[stale.ID] {
		t.Fatalf("expected the genuinely stale reservation to be released by RunOnce, status stayed unreleased")
	}
	if released[fresh.ID] {
		t.Fatalf("RunOnce released a reservation created moments ago; ListStaleReservations must only match rows older than the TTL cutoff")
	}
	if released[inFlightBatch.ID] {
		t.Fatalf("RunOnce released a hold whose batch is still in_progress; the batch-exclusion subquery must keep this reservation out of the candidate scan")
	}
	if !released[doneBatch.ID] {
		t.Fatalf("RunOnce left a stale hold alone because its batch was 'completed'; a terminal batch status must not exclude the reservation from reaping")
	}

	if result.Scanned < 2 {
		t.Fatalf("expected RunOnce to have scanned at least the two eligible stale reservations (stale, doneBatch), got Scanned=%d", result.Scanned)
	}
	if result.Released < 2 {
		t.Fatalf("expected RunOnce to report at least 2 released, got %d", result.Released)
	}

	// The released hold's credits must actually be back on the books, read
	// straight off GetBalance rather than trusting the reservation row alone.
	balance, err := ledgerSvc.GetBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	// Only the in-flight batch's 9000 should still be held: stale (4000),
	// fresh is not reaped but is still open (1500), and doneBatch (2500) was
	// released. So reserved = fresh's 1500 + inFlightBatch's 9000 = 10500.
	const wantReserved = int64(1500 + 9000)
	if balance.ReservedCredits != wantReserved {
		t.Fatalf("reserved credits after reaping = %d, want %d (fresh 1500 + in-flight-batch 9000 still held; stale 4000 and doneBatch 2500 released)",
			balance.ReservedCredits, wantReserved)
	}
}

func createTestReservation(t *testing.T, ctx context.Context, svc *Service, accountID uuid.UUID, endpoint, alias string, credits int64) Reservation {
	t.Helper()
	reservation, err := svc.CreateReservation(ctx, CreateReservationInput{
		AccountID:        accountID,
		RequestID:        uuid.NewString(),
		AttemptNumber:    1,
		Endpoint:         endpoint,
		ModelAlias:       alias,
		EstimatedCredits: credits,
	})
	if err != nil {
		t.Fatalf("CreateReservation: %v", err)
	}
	return reservation
}

// backdateReservation pushes created_at and updated_at back by `age`, the only
// way to make a reservation look abandoned to ListStaleReservations without
// waiting out a real TTL. Both columns move together, matching what a
// never-touched-since-creation row looks like; the query requires both to
// precede the cutoff.
func backdateReservation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, reservationID uuid.UUID, age time.Duration) {
	t.Helper()
	cutoff := time.Now().UTC().Add(-age)
	if _, err := pool.Exec(ctx,
		`UPDATE public.credit_reservations SET created_at = $2, updated_at = $2 WHERE id = $1`,
		reservationID, cutoff); err != nil {
		t.Fatalf("backdate reservation: %v", err)
	}
}

// seedBatchRow inserts the minimum public.files + public.batches rows needed
// to exercise ListStaleReservations' batch-exclusion subquery, which joins on
// b.reservation_id = cr.id::text. Real column types, real cast, no shortcuts:
// a mismatch here is exactly what would make the subquery silently match
// nothing and let a genuinely in-flight batch's hold be reaped out from under it.
func seedBatchRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, accountID, reservationID uuid.UUID, status string) {
	t.Helper()
	fileID := "file-" + uuid.NewString()
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.files (id, account_id, purpose, filename, bytes, storage_path)
		 VALUES ($1, $2, 'batch', 'input.jsonl', 10, 'test/'||$1)`,
		fileID, accountID.String()); err != nil {
		t.Fatalf("seed batch input file: %v", err)
	}
	batchID := "batch-" + uuid.NewString()
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.batches (id, account_id, input_file_id, endpoint, status, reservation_id)
		 VALUES ($1, $2, $3, '/v1/chat/completions', $4, $5)`,
		batchID, accountID.String(), fileID, status, reservationID.String()); err != nil {
		t.Fatalf("seed batch row: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM public.batches WHERE id = $1`, batchID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM public.files WHERE id = $1`, fileID)
	})
}
