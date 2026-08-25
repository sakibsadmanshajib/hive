package accounting

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usage"
)

// serializingLedger simulates the real ledger: GetBalance reads the live
// available balance and ReserveCredits posts a hold that decrements it. The
// available balance is the shared mutable state that the TOCTOU race corrupts.
type serializingLedger struct {
	mu        sync.Mutex
	available int64
	holds     int64
	// Barrier state for the negative control: GetBalance counts reads under
	// mu; once reads reaches barrierTarget the channel closes, and every
	// ReserveCredits blocks on it until then. This makes "all balance reads
	// land before any reserve" deterministic instead of scheduler-dependent,
	// which is what the fixed sleep approximated.
	readBarrierTarget int64
	reads             int64
	barrier           chan struct{}
}

func (l *serializingLedger) GetBalance(_ context.Context, _ uuid.UUID) (ledger.BalanceSummary, error) {
	l.mu.Lock()
	available := l.available
	l.reads++
	// Close exactly once: reads only grows and the comparison is equality,
	// so later calls land strictly past the target. The channel is never
	// mutated after construction, so ReserveCredits may read it without the
	// lock.
	if l.readBarrierTarget > 0 && l.reads == l.readBarrierTarget && l.barrier != nil {
		close(l.barrier)
	}
	l.mu.Unlock()
	return ledger.BalanceSummary{PostedCredits: available, AvailableCredits: available}, nil
}

func (l *serializingLedger) ReserveCredits(_ context.Context, _ uuid.UUID, _ string, _, _ *uuid.UUID, _ string, credits int64, _ map[string]any) (ledger.LedgerEntry, error) {
	// Wait for every goroutine's balance read to land before the first hold
	// posts, so the over-reservation reproduces deterministically. The
	// bounded fallback exists so a broken barrier can never hang CI; if it
	// ever fires, the assertions below fail loudly instead.
	if l.barrier != nil {
		select {
		case <-l.barrier:
		case <-time.After(5 * time.Second):
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.available -= credits
	l.holds++
	return ledger.LedgerEntry{ID: uuid.New(), EntryType: ledger.EntryTypeReservationHold, CreditsDelta: -credits}, nil
}

func (l *serializingLedger) ReleaseReservedCredits(_ context.Context, _ uuid.UUID, _ string, _, _ *uuid.UUID, _ string, credits int64, _ map[string]any) (ledger.LedgerEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.available += credits
	return ledger.LedgerEntry{ID: uuid.New(), EntryType: ledger.EntryTypeReservationRelease, CreditsDelta: credits}, nil
}

func (l *serializingLedger) ChargeUsage(_ context.Context, _ uuid.UUID, _ string, _, _ *uuid.UUID, _ string, credits int64, _ map[string]any) (ledger.LedgerEntry, error) {
	return ledger.LedgerEntry{ID: uuid.New(), EntryType: ledger.EntryTypeUsageCharge, CreditsDelta: -credits}, nil
}

func (l *serializingLedger) RefundCredits(_ context.Context, _ uuid.UUID, _ string, _, _ *uuid.UUID, _ string, credits int64, _ map[string]any) (ledger.LedgerEntry, error) {
	return ledger.LedgerEntry{ID: uuid.New(), EntryType: ledger.EntryTypeRefund, CreditsDelta: credits}, nil
}

// concurrentRepo is a goroutine-safe repoStub for the race test. It applies the
// hold to the shared balance itself, because the production repository writes
// the hold in the same transaction as the row (issue #918); modelling it
// anywhere else would leave the race test measuring a sequence the system no
// longer performs.
type concurrentRepo struct {
	mu     sync.Mutex
	count  int
	ledger *serializingLedger
}

func (r *concurrentRepo) CreateReservation(ctx context.Context, reservation Reservation, _ string, hold ReservationHold) (Reservation, error) {
	r.mu.Lock()
	r.count++
	r.mu.Unlock()
	if r.ledger != nil {
		if _, err := r.ledger.ReserveCredits(ctx, reservation.AccountID, reservation.RequestID, nil, &reservation.ID, hold.IdempotencyKey, hold.Credits, hold.Metadata); err != nil {
			return Reservation{}, err
		}
	}
	return reservation, nil
}
func (r *concurrentRepo) GetReservation(context.Context, uuid.UUID, uuid.UUID) (Reservation, error) {
	return Reservation{}, ErrNotFound
}
func (r *concurrentRepo) ExpandReservation(context.Context, uuid.UUID, uuid.UUID, int64, string, ReservationHold) (Reservation, error) {
	return Reservation{}, ErrNotFound
}
func (r *concurrentRepo) FinalizeReservation(context.Context, uuid.UUID, uuid.UUID, int64, int64, bool, ReservationStatus, string) (Reservation, error) {
	return Reservation{}, ErrNotFound
}
func (r *concurrentRepo) ReleaseReservation(context.Context, uuid.UUID, uuid.UUID, int64, string) (Reservation, error) {
	return Reservation{}, ErrNotFound
}
func (r *concurrentRepo) CreateReconciliationJob(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}
func (r *concurrentRepo) ListStaleReservations(context.Context, time.Time, int) ([]Reservation, error) {
	return nil, nil
}

// concurrentUsage is a goroutine-safe usage stub.
type concurrentUsage struct{}

func (concurrentUsage) StartAttempt(_ context.Context, input usage.StartAttemptInput) (usage.RequestAttempt, error) {
	return usage.RequestAttempt{ID: uuid.New(), AccountID: input.AccountID, RequestID: input.RequestID, Status: input.Status}, nil
}
func (concurrentUsage) UpdateAttemptStatus(context.Context, uuid.UUID, usage.AttemptStatus, *time.Time) error {
	return nil
}
func (concurrentUsage) RecordEvent(_ context.Context, input usage.RecordEventInput) (usage.UsageEvent, error) {
	return usage.UsageEvent{ID: uuid.New()}, nil
}
func (concurrentUsage) ListAttempts(context.Context, uuid.UUID, string, int) ([]usage.RequestAttempt, error) {
	return nil, nil
}

// runConcurrentReservations fires n concurrent strict reservations of `each`
// credits against a single account starting with `balance` available credits,
// using the supplied account locker, and returns how many succeeded. Optional
// ledger prep hooks run after construction but before the goroutines launch,
// so a test can arm the read barrier without racing the workers.
func runConcurrentReservations(t *testing.T, locker AccountLocker, balance, each int64, n int, prep ...func(*serializingLedger)) (int, *serializingLedger) {
	t.Helper()
	ledgerSvc := &serializingLedger{available: balance}
	for _, p := range prep {
		p(ledgerSvc)
	}
	repo := &concurrentRepo{ledger: ledgerSvc}
	svc := NewService(repo, ledgerSvc, concurrentUsage{}).WithAccountLocker(locker)

	accountID := uuid.New()
	var success int64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := svc.CreateReservation(context.Background(), CreateReservationInput{
				AccountID:        accountID,
				RequestID:        fmt.Sprintf("req_%d", i),
				AttemptNumber:    1,
				Endpoint:         "/v1/responses",
				ModelAlias:       "hive-fast",
				EstimatedCredits: each,
				PolicyMode:       PolicyModeStrict,
			})
			if err == nil {
				atomic.AddInt64(&success, 1)
			}
		}(i)
	}
	wg.Wait()
	return int(success), ledgerSvc
}

// TestCreateReservationSerializesConcurrentReservations is the #106 acceptance
// test: balance=1000, reserve=50, 100 concurrent → at most 20 succeed.
func TestCreateReservationSerializesConcurrentReservations(t *testing.T) {
	success, ledgerSvc := runConcurrentReservations(t, NewProcessAccountLocker(), 1000, 50, 100)

	if success > 20 {
		t.Fatalf("strict policy over-reserved under concurrency: %d reservations succeeded (want <= 20)", success)
	}
	if ledgerSvc.available < 0 {
		t.Fatalf("available balance went negative (%d): credits double-spent", ledgerSvc.available)
	}
}

// TestNoopLockerOverReserves proves the acceptance test discriminates: without
// per-account serialization the same workload double-spends. serializingLedger
// holds every reserve until all 100 balance reads have landed (the read
// barrier armed below), so this reproduces deterministically rather than
// depending on scheduler interleaving: a negative control that can pass by
// doing nothing contributes no coverage at all.
func TestNoopLockerOverReserves(t *testing.T) {
	success, ledgerSvc := runConcurrentReservations(t, noopAccountLocker{}, 1000, 50, 100,
		func(l *serializingLedger) {
			l.readBarrierTarget = 100
			l.barrier = make(chan struct{})
		})
	if success <= 20 {
		t.Fatalf("noop locker failed to reproduce the double-spend it exists to prove: only %d of 100 concurrent reservations succeeded (want >20 without per-account locking)", success)
	}
	if ledgerSvc.available >= 0 {
		t.Fatalf("expected the noop locker to over-reserve the ledger into negative available credits, got %d", ledgerSvc.available)
	}
}

// countingLocker records how many times the per-account lock is taken.
type countingLocker struct {
	mu       sync.Mutex
	calls    int
	accounts []uuid.UUID
	// held is true while the guarded function runs, so a fake repository can
	// record whether IT ran inside the lock. Without it the assertion fires
	// only if the service stops taking a hold at all, and moving the repository
	// call outside WithAccountLock would keep the test green.
	held bool
}

func (l *countingLocker) WithAccountLock(ctx context.Context, accountID uuid.UUID, fn func(context.Context) error) error {
	l.mu.Lock()
	l.calls++
	l.accounts = append(l.accounts, accountID)
	l.held = true
	l.mu.Unlock()

	err := fn(ctx)

	l.mu.Lock()
	l.held = false
	l.mu.Unlock()
	return err
}

func (l *countingLocker) heldNow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held
}

// TestCreateReservationAcquiresAccountLock deterministically proves the
// balance-read → hold critical section runs inside the per-account lock, so the
// #106 serialization cannot be silently removed without failing a test.
func TestCreateReservationAcquiresAccountLock(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{balance: ledger.BalanceSummary{AvailableCredits: 500}}
	locker := &countingLocker{}
	accountID := uuid.New()

	repo.lockProbe = locker
	svc := NewService(repo, ledgerSvc, &usageStub{}).WithAccountLocker(locker)
	if _, err := svc.CreateReservation(context.Background(), CreateReservationInput{
		AccountID:        accountID,
		RequestID:        "req_lock",
		AttemptNumber:    1,
		Endpoint:         "/v1/responses",
		ModelAlias:       "hive-fast",
		EstimatedCredits: 50,
		PolicyMode:       PolicyModeStrict,
	}); err != nil {
		t.Fatalf("CreateReservation returned error: %v", err)
	}

	if locker.calls != 1 {
		t.Fatalf("expected reservation to acquire the account lock exactly once, got %d", locker.calls)
	}
	if len(locker.accounts) != 1 || locker.accounts[0] != accountID {
		t.Fatalf("expected lock keyed on account %s, got %#v", accountID, locker.accounts)
	}
	// The hold is written with the row now (issue #918), so the thing that must
	// happen inside the lock is the repository call that carries it. Asserted
	// on where it ran, not merely that it ran: repoStub records the locker's
	// held flag at call time, so moving s.repo.CreateReservation outside
	// WithAccountLock fails this.
	if len(repo.holds) != 1 || repo.holds[0].Credits != 50 {
		t.Fatalf("expected the 50-credit hold to be written inside the lock, got %#v", repo.holds)
	}
	if !repo.createdUnderLock {
		t.Fatal("the reservation and its hold were written outside the per-account lock, which is the double-spend window issue #106 closed")
	}
}

// TestBalanceAffectingPathsAcquireAccountLock proves Finalize and Release —
// which post charge/release ledger entries that change available balance — also
// run inside the per-account lock, closing the double-spend window against a
// concurrent create (issue #106 follow-up review).
func TestBalanceAffectingPathsAcquireAccountLock(t *testing.T) {
	accountID := uuid.New()

	for _, tc := range []struct {
		name string
		run  func(svc *Service, reservationID uuid.UUID) error
	}{
		{"finalize", func(svc *Service, id uuid.UUID) error {
			_, err := svc.FinalizeReservation(context.Background(), FinalizeReservationInput{
				AccountID: accountID, ReservationID: id, ActualCredits: 40,
				TerminalUsageConfirmed: true, Status: string(usage.AttemptStatusCompleted),
			})
			return err
		}},
		{"release", func(svc *Service, id uuid.UUID) error {
			_, err := svc.ReleaseReservation(context.Background(), ReleaseReservationInput{
				AccountID: accountID, ReservationID: id, Reason: "cancelled",
			})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepoStub()
			reservationID := uuid.New()
			repo.reservations[reservationID] = Reservation{
				ID: reservationID, AccountID: accountID, RequestAttemptID: uuid.New(),
				ReservationKey: "k:1", PolicyMode: PolicyModeStrict,
				Status: ReservationStatusActive, ReservedCredits: 100,
			}
			locker := &countingLocker{}
			svc := NewService(repo, &ledgerStub{}, &usageStub{}).WithAccountLocker(locker)

			if err := tc.run(svc, reservationID); err != nil {
				t.Fatalf("%s returned error: %v", tc.name, err)
			}
			if locker.calls != 1 {
				t.Fatalf("expected %s to acquire the account lock exactly once, got %d", tc.name, locker.calls)
			}
			if len(locker.accounts) != 1 || locker.accounts[0] != accountID {
				t.Fatalf("expected %s lock keyed on account %s, got %#v", tc.name, accountID, locker.accounts)
			}
		})
	}
}
