package accounting

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
)

// reaperLedger models the parts of the real ledger the reaper depends on:
// an append-only entry log deduplicated by idempotency key (the production
// guard is the credit_idempotency_keys primary key plus ON CONFLICT DO
// NOTHING in ledger/repository.go), and a balance derived from those entries.
// Tests assert on the entry log rather than on return values, because a
// duplicate release returns the existing entry and is indistinguishable from a
// fresh one at the call site.
type reaperLedger struct {
	mu       sync.Mutex
	posted   int64
	held     int64
	released int64
	seenKeys map[string]bool
	entries  []ledger.LedgerEntry
}

func newReaperLedger(grant int64) *reaperLedger {
	return &reaperLedger{posted: grant, seenKeys: make(map[string]bool)}
}

// post applies an entry unless its (entry type, idempotency key) pair has
// already been written, mirroring the production ON CONFLICT DO NOTHING path.
func (l *reaperLedger) post(entryType ledger.EntryType, key string, delta int64, apply func()) ledger.LedgerEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	dedupeKey := string(entryType) + "|" + key
	if l.seenKeys[dedupeKey] {
		for _, existing := range l.entries {
			if existing.EntryType == entryType && existing.IdempotencyKey == key {
				return existing
			}
		}
	}
	l.seenKeys[dedupeKey] = true
	apply()
	entry := ledger.LedgerEntry{ID: uuid.New(), EntryType: entryType, CreditsDelta: delta, IdempotencyKey: key}
	l.entries = append(l.entries, entry)
	return entry
}

func (l *reaperLedger) GetBalance(_ context.Context, _ uuid.UUID) (ledger.BalanceSummary, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	reserved := l.held - l.released
	return ledger.BalanceSummary{
		PostedCredits:    l.posted,
		ReservedCredits:  reserved,
		AvailableCredits: l.posted - reserved,
	}, nil
}

func (l *reaperLedger) ReserveCredits(_ context.Context, _ uuid.UUID, _ string, _, _ *uuid.UUID, key string, credits int64, _ map[string]any) (ledger.LedgerEntry, error) {
	return l.post(ledger.EntryTypeReservationHold, key, -credits, func() { l.held += credits }), nil
}

func (l *reaperLedger) ReleaseReservedCredits(_ context.Context, _ uuid.UUID, _ string, _, _ *uuid.UUID, key string, credits int64, _ map[string]any) (ledger.LedgerEntry, error) {
	return l.post(ledger.EntryTypeReservationRelease, key, credits, func() { l.released += credits }), nil
}

func (l *reaperLedger) ChargeUsage(_ context.Context, _ uuid.UUID, _ string, _, _ *uuid.UUID, key string, credits int64, _ map[string]any) (ledger.LedgerEntry, error) {
	return l.post(ledger.EntryTypeUsageCharge, key, -credits, func() { l.posted -= credits }), nil
}

func (l *reaperLedger) RefundCredits(_ context.Context, _ uuid.UUID, _ string, _, _ *uuid.UUID, key string, credits int64, _ map[string]any) (ledger.LedgerEntry, error) {
	return l.post(ledger.EntryTypeRefund, key, credits, func() { l.posted += credits }), nil
}

// entryCount counts the ledger rows of a given type, which is the exactly-once
// assertion surface.
func (l *reaperLedger) entryCount(entryType ledger.EntryType) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	count := 0
	for _, entry := range l.entries {
		if entry.EntryType == entryType {
			count++
		}
	}
	return count
}

// seedReservation inserts a reservation directly with an explicit age, standing
// in for a hold whose request died before reaching a terminal state.
func seedReservation(repo *repoStub, accountID uuid.UUID, credits int64, status ReservationStatus, age time.Duration) Reservation {
	at := time.Now().UTC().Add(-age)
	reservation := Reservation{
		ID:               uuid.New(),
		AccountID:        accountID,
		RequestAttemptID: uuid.New(),
		ReservationKey:   buildReservationKey(accountID, uuid.NewString(), 1),
		RequestID:        uuid.NewString(),
		AttemptNumber:    1,
		Endpoint:         "/v1/chat/completions",
		ModelAlias:       "hive-fast",
		PolicyMode:       PolicyModeStrict,
		Status:           status,
		ReservedCredits:  credits,
		CreatedAt:        at,
		UpdatedAt:        at,
	}
	repo.reservations[reservation.ID] = reservation
	return reservation
}

func newReaperFixture(t *testing.T, grant int64) (*Reaper, *repoStub, *reaperLedger) {
	t.Helper()
	repo := newRepoStub()
	ledgerSvc := newReaperLedger(grant)
	svc := NewService(repo, ledgerSvc, concurrentUsage{})
	reaper := NewReaper(repo, svc, ReaperConfig{TTL: ReaperDefaultTTL})
	return reaper, repo, ledgerSvc
}

// (a) A hold whose request never reached a terminal state, older than the TTL,
// is released, and (e) the credits return to the account balance.
func TestReaperReleasesStrandedHoldPastTTL(t *testing.T) {
	reaper, repo, ledgerSvc := newReaperFixture(t, 300000)
	accountID := uuid.New()
	reservation := seedReservation(repo, accountID, 10000, ReservationStatusActive, 6*time.Hour)

	// Post the original hold so the balance reflects the stranded reservation.
	if _, err := ledgerSvc.ReserveCredits(context.Background(), accountID, reservation.RequestID, nil, &reservation.ID, "reservation:"+reservation.ID.String()+":reserve", 10000, nil); err != nil {
		t.Fatalf("seed hold: %v", err)
	}
	before, _ := ledgerSvc.GetBalance(context.Background(), accountID)
	if before.AvailableCredits != 290000 {
		t.Fatalf("fixture wrong: available before reap = %d, want 290000", before.AvailableCredits)
	}

	result, err := reaper.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if result.Released != 1 {
		t.Fatalf("expected 1 stranded hold released, got %d", result.Released)
	}
	if result.Credits != 10000 {
		t.Fatalf("expected 10000 credits released, got %d", result.Credits)
	}

	stored, err := repo.GetReservation(context.Background(), accountID, reservation.ID)
	if err != nil {
		t.Fatalf("reload reservation: %v", err)
	}
	if stored.Status != ReservationStatusReleased {
		t.Fatalf("expected reservation status released, got %q", stored.Status)
	}

	after, _ := ledgerSvc.GetBalance(context.Background(), accountID)
	if after.AvailableCredits != 300000 {
		t.Fatalf("released credits did not return to the balance: available = %d, want 300000", after.AvailableCredits)
	}
	if after.ReservedCredits != 0 {
		t.Fatalf("expected reserved credits to return to zero, got %d", after.ReservedCredits)
	}
}

// (b) The invariant guard: a hold for a request that is still in flight is not
// released. In flight is defined as younger than the TTL, because a stuck
// attempt row is exactly what makes a hold stranded, so attempt status alone
// cannot distinguish the two.
func TestReaperLeavesInFlightHoldAlone(t *testing.T) {
	reaper, repo, ledgerSvc := newReaperFixture(t, 300000)
	accountID := uuid.New()
	reservation := seedReservation(repo, accountID, 10000, ReservationStatusActive, 30*time.Second)

	result, err := reaper.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if result.Released != 0 {
		t.Fatalf("reaper released an in-flight hold: released = %d", result.Released)
	}
	if got := ledgerSvc.entryCount(ledger.EntryTypeReservationRelease); got != 0 {
		t.Fatalf("reaper posted %d release entries for an in-flight hold, want 0", got)
	}

	stored, err := repo.GetReservation(context.Background(), accountID, reservation.ID)
	if err != nil {
		t.Fatalf("reload reservation: %v", err)
	}
	if stored.Status != ReservationStatusActive {
		t.Fatalf("expected in-flight reservation to stay active, got %q", stored.Status)
	}

	// Positive control: the same fixture is reapable, so the assertions above
	// prove the TTL held it back rather than that nothing was reapable at all.
	// Without this a broken reaper that releases nothing would pass the test.
	eager := NewReaper(repo, NewService(repo, ledgerSvc, concurrentUsage{}), ReaperConfig{TTL: time.Nanosecond})
	control, err := eager.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("control RunOnce returned error: %v", err)
	}
	if control.Released != 1 {
		t.Fatalf("control: the same hold should be reapable with a zero TTL, released = %d", control.Released)
	}
}

// (c) A hold that already reached a terminal state is not released, twice over:
// the candidate query excludes it, and if one ever slipped through, the service
// backstop at service.go refuses to release a finalized reservation.
func TestReaperLeavesFinalizedHoldAlone(t *testing.T) {
	t.Run("excluded from candidates", func(t *testing.T) {
		reaper, repo, ledgerSvc := newReaperFixture(t, 300000)
		accountID := uuid.New()
		seedReservation(repo, accountID, 10000, ReservationStatusFinalized, 6*time.Hour)
		seedReservation(repo, accountID, 10000, ReservationStatusReleased, 6*time.Hour)

		result, err := reaper.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce returned error: %v", err)
		}
		if result.Scanned != 0 {
			t.Fatalf("terminal reservations were scanned as candidates: %d", result.Scanned)
		}
		if got := ledgerSvc.entryCount(ledger.EntryTypeReservationRelease); got != 0 {
			t.Fatalf("reaper posted %d release entries for terminal holds, want 0", got)
		}
	})

	t.Run("service backstop refuses a finalized hold", func(t *testing.T) {
		repo := newRepoStub()
		ledgerSvc := newReaperLedger(300000)
		svc := NewService(repo, ledgerSvc, concurrentUsage{})
		accountID := uuid.New()
		finalized := seedReservation(repo, accountID, 10000, ReservationStatusFinalized, 6*time.Hour)

		// A lister that hands the reaper a finalized reservation anyway, which is
		// what a widened query or a race with a concurrent finalize would do.
		reaper := NewReaper(staleListerFunc(func(context.Context, time.Time, int) ([]Reservation, error) {
			return []Reservation{finalized}, nil
		}), svc, ReaperConfig{TTL: ReaperDefaultTTL})

		result, err := reaper.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce returned error: %v", err)
		}
		if result.Released != 0 {
			t.Fatalf("reaper released a finalized hold: released = %d", result.Released)
		}
		if result.Failed != 1 {
			t.Fatalf("expected the refused release to be counted as a failure, got %d", result.Failed)
		}
		if got := ledgerSvc.entryCount(ledger.EntryTypeReservationRelease); got != 0 {
			t.Fatalf("reaper posted %d release entries for a finalized hold, want 0", got)
		}
	})
}

// (d) Two overlapping passes must retire the hold exactly once. The assertion is
// on ledger rows, not on the two return values, because the second pass gets the
// first pass's entry back and cannot tell it apart from its own.
func TestConcurrentReaperRunsReleaseExactlyOnce(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := newReaperLedger(300000)
	svc := NewService(repo, ledgerSvc, concurrentUsage{})
	accountID := uuid.New()
	reservation := seedReservation(repo, accountID, 10000, ReservationStatusActive, 6*time.Hour)
	if _, err := ledgerSvc.ReserveCredits(context.Background(), accountID, reservation.RequestID, nil, &reservation.ID, "reservation:"+reservation.ID.String()+":reserve", 10000, nil); err != nil {
		t.Fatalf("seed hold: %v", err)
	}

	first := NewReaper(repo, svc, ReaperConfig{TTL: ReaperDefaultTTL})
	second := NewReaper(repo, svc, ReaperConfig{TTL: ReaperDefaultTTL})

	var wg sync.WaitGroup
	wg.Add(2)
	for _, reaper := range []*Reaper{first, second} {
		go func(r *Reaper) {
			defer wg.Done()
			if _, err := r.RunOnce(context.Background()); err != nil {
				t.Errorf("concurrent RunOnce returned error: %v", err)
			}
		}(reaper)
	}
	wg.Wait()

	if got := ledgerSvc.entryCount(ledger.EntryTypeReservationRelease); got != 1 {
		t.Fatalf("expected exactly 1 release ledger entry from two concurrent passes, got %d", got)
	}
	balance, _ := ledgerSvc.GetBalance(context.Background(), accountID)
	if balance.AvailableCredits != 300000 {
		t.Fatalf("double release corrupted the balance: available = %d, want 300000", balance.AvailableCredits)
	}
}

// staleListerFunc adapts a function to the reaper's candidate source.
type staleListerFunc func(ctx context.Context, olderThan time.Time, limit int) ([]Reservation, error)

func (f staleListerFunc) ListStaleReservations(ctx context.Context, olderThan time.Time, limit int) ([]Reservation, error) {
	return f(ctx, olderThan, limit)
}
