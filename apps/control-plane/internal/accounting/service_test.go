package accounting

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usage"
)

type ledgerCall struct {
	accountID      uuid.UUID
	requestID      string
	attemptID      *uuid.UUID
	reservationID  *uuid.UUID
	idempotencyKey string
	credits        int64
	metadata       map[string]any
}

type ledgerStub struct {
	balance      ledger.BalanceSummary
	reserveCalls []ledgerCall
	releaseCalls []ledgerCall
	chargeCalls  []ledgerCall
	refundCalls  []ledgerCall
}

func (l *ledgerStub) GetBalance(_ context.Context, _ uuid.UUID) (ledger.BalanceSummary, error) {
	return l.balance, nil
}

func (l *ledgerStub) ReserveCredits(_ context.Context, accountID uuid.UUID, requestID string, attemptID, reservationID *uuid.UUID, idempotencyKey string, credits int64, metadata map[string]any) (ledger.LedgerEntry, error) {
	l.reserveCalls = append(l.reserveCalls, ledgerCall{
		accountID:      accountID,
		requestID:      requestID,
		attemptID:      attemptID,
		reservationID:  reservationID,
		idempotencyKey: idempotencyKey,
		credits:        credits,
		metadata:       metadata,
	})
	return ledger.LedgerEntry{ID: uuid.New(), EntryType: ledger.EntryTypeReservationHold, CreditsDelta: -credits}, nil
}

func (l *ledgerStub) ReleaseReservedCredits(_ context.Context, accountID uuid.UUID, requestID string, attemptID, reservationID *uuid.UUID, idempotencyKey string, credits int64, metadata map[string]any) (ledger.LedgerEntry, error) {
	l.releaseCalls = append(l.releaseCalls, ledgerCall{
		accountID:      accountID,
		requestID:      requestID,
		attemptID:      attemptID,
		reservationID:  reservationID,
		idempotencyKey: idempotencyKey,
		credits:        credits,
		metadata:       metadata,
	})
	return ledger.LedgerEntry{ID: uuid.New(), EntryType: ledger.EntryTypeReservationRelease, CreditsDelta: credits}, nil
}

func (l *ledgerStub) ChargeUsage(_ context.Context, accountID uuid.UUID, requestID string, attemptID, reservationID *uuid.UUID, idempotencyKey string, credits int64, metadata map[string]any) (ledger.LedgerEntry, error) {
	l.chargeCalls = append(l.chargeCalls, ledgerCall{
		accountID:      accountID,
		requestID:      requestID,
		attemptID:      attemptID,
		reservationID:  reservationID,
		idempotencyKey: idempotencyKey,
		credits:        credits,
		metadata:       metadata,
	})
	return ledger.LedgerEntry{ID: uuid.New(), EntryType: ledger.EntryTypeUsageCharge, CreditsDelta: -credits}, nil
}

func (l *ledgerStub) RefundCredits(_ context.Context, accountID uuid.UUID, requestID string, attemptID, reservationID *uuid.UUID, idempotencyKey string, credits int64, metadata map[string]any) (ledger.LedgerEntry, error) {
	l.refundCalls = append(l.refundCalls, ledgerCall{
		accountID:      accountID,
		requestID:      requestID,
		attemptID:      attemptID,
		reservationID:  reservationID,
		idempotencyKey: idempotencyKey,
		credits:        credits,
		metadata:       metadata,
	})
	return ledger.LedgerEntry{ID: uuid.New(), EntryType: ledger.EntryTypeRefund, CreditsDelta: credits}, nil
}

type usageStatusCall struct {
	attemptID   uuid.UUID
	status      usage.AttemptStatus
	completedAt *time.Time
}

type usageStub struct {
	startCalls  []usage.StartAttemptInput
	statusCalls []usageStatusCall
	eventCalls  []usage.RecordEventInput
	attempts    []usage.RequestAttempt
}

func (u *usageStub) StartAttempt(_ context.Context, input usage.StartAttemptInput) (usage.RequestAttempt, error) {
	u.startCalls = append(u.startCalls, input)
	attempt := usage.RequestAttempt{
		ID:            uuid.New(),
		AccountID:     input.AccountID,
		RequestID:     input.RequestID,
		AttemptNumber: input.AttemptNumber,
		Endpoint:      input.Endpoint,
		ModelAlias:    input.ModelAlias,
		Status:        input.Status,
		APIKeyID:      input.APIKeyID,
		CustomerTags:  input.CustomerTags,
		StartedAt:     time.Now().UTC(),
	}
	u.attempts = append(u.attempts, attempt)
	return attempt, nil
}

func (u *usageStub) UpdateAttemptStatus(_ context.Context, attemptID uuid.UUID, status usage.AttemptStatus, completedAt *time.Time) error {
	u.statusCalls = append(u.statusCalls, usageStatusCall{attemptID: attemptID, status: status, completedAt: completedAt})
	return nil
}

func (u *usageStub) RecordEvent(_ context.Context, input usage.RecordEventInput) (usage.UsageEvent, error) {
	u.eventCalls = append(u.eventCalls, input)
	return usage.UsageEvent{
		ID:               uuid.New(),
		AccountID:        input.AccountID,
		RequestAttemptID: input.RequestAttemptID,
		APIKeyID:         input.APIKeyID,
		RequestID:        input.RequestID,
		EventType:        input.EventType,
		Status:           input.Status,
	}, nil
}

func (u *usageStub) ListAttempts(_ context.Context, accountID uuid.UUID, requestID string, limit int) ([]usage.RequestAttempt, error) {
	var attempts []usage.RequestAttempt
	for _, attempt := range u.attempts {
		if attempt.AccountID == accountID && (requestID == "" || attempt.RequestID == requestID) {
			attempts = append(attempts, attempt)
		}
	}
	if limit > 0 && len(attempts) > limit {
		return append([]usage.RequestAttempt(nil), attempts[:limit]...), nil
	}
	return append([]usage.RequestAttempt(nil), attempts...), nil
}

type apiKeyDeltaCall struct {
	apiKeyID      uuid.UUID
	budgetKind    string
	reservedDelta int64
	consumedDelta int64
	at            time.Time
}

type apiKeyUsageCall struct {
	apiKeyID        uuid.UUID
	modelAlias      string
	consumedCredits int64
	at              time.Time
}

type apiKeyStub struct {
	budgetKindByKey   map[uuid.UUID]string
	deltaCalls        []apiKeyDeltaCall
	finalizationCalls []apiKeyUsageCall
	lastUsedCalls     []struct {
		apiKeyID uuid.UUID
		at       time.Time
	}
}

func (a *apiKeyStub) ApplyReservationDelta(_ context.Context, apiKeyID uuid.UUID, reservedDelta int64, consumedDelta int64, at time.Time) error {
	budgetKind := a.budgetKindByKey[apiKeyID]
	if budgetKind == "" {
		budgetKind = "lifetime"
	}
	a.deltaCalls = append(a.deltaCalls, apiKeyDeltaCall{
		apiKeyID:      apiKeyID,
		budgetKind:    budgetKind,
		reservedDelta: reservedDelta,
		consumedDelta: consumedDelta,
		at:            at,
	})
	return nil
}

func (a *apiKeyStub) RecordUsageFinalization(_ context.Context, apiKeyID uuid.UUID, modelAlias string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, consumedCredits int64, at time.Time) error {
	a.finalizationCalls = append(a.finalizationCalls, apiKeyUsageCall{
		apiKeyID:        apiKeyID,
		modelAlias:      modelAlias,
		consumedCredits: consumedCredits,
		at:              at,
	})
	return nil
}

func (a *apiKeyStub) MarkLastUsed(_ context.Context, apiKeyID uuid.UUID, at time.Time) error {
	a.lastUsedCalls = append(a.lastUsedCalls, struct {
		apiKeyID uuid.UUID
		at       time.Time
	}{apiKeyID: apiKeyID, at: at})
	return nil
}

type reconciliationJob struct {
	reservationID    uuid.UUID
	requestAttemptID uuid.UUID
	reason           string
}

// repoStub is guarded by a mutex so the concurrent reaper test can drive two
// passes at once without racing on the maps.
type repoStub struct {
	mu                 sync.Mutex
	reservations       map[uuid.UUID]Reservation
	reconciliationJobs []reconciliationJob
	releaseEventCounts map[uuid.UUID]int
	holds              []ReservationHold

	// Optional fake ledger. The production repository posts the hold in its own
	// transaction (issue #918), so a test whose subject is the account balance
	// has to see the hold land here, or its fixture silently starts from an
	// unreserved balance.
	ledger holdPoster
}

type holdPoster interface {
	ReserveCredits(ctx context.Context, accountID uuid.UUID, requestID string, attemptID, reservationID *uuid.UUID, idempotencyKey string, credits int64, metadata map[string]any) (ledger.LedgerEntry, error)
}

func newRepoStub() *repoStub {
	return &repoStub{
		reservations:       make(map[uuid.UUID]Reservation),
		releaseEventCounts: make(map[uuid.UUID]int),
	}
}

// CreateReservation records the hold alongside the row, because the production
// repository now writes both in one transaction (issue #918). Tests assert on
// r.holds where they used to assert on the ledger's reserve calls; without
// recording it here, the assertion that a hold is taken at all would quietly
// stop checking anything.
func (r *repoStub) CreateReservation(ctx context.Context, reservation Reservation, reason string, hold ReservationHold) (Reservation, error) {
	r.mu.Lock()
	now := time.Now().UTC()
	reservation.CreatedAt = now
	reservation.UpdatedAt = now
	r.reservations[reservation.ID] = reservation
	r.holds = append(r.holds, hold)
	poster := r.ledger
	r.mu.Unlock()

	if poster != nil {
		if _, err := poster.ReserveCredits(ctx, reservation.AccountID, reservation.RequestID, &reservation.RequestAttemptID, &reservation.ID, hold.IdempotencyKey, hold.Credits, hold.Metadata); err != nil {
			return Reservation{}, err
		}
	}
	return reservation, nil
}

func (r *repoStub) GetReservation(_ context.Context, accountID, reservationID uuid.UUID) (Reservation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.getLocked(accountID, reservationID)
}

func (r *repoStub) getLocked(accountID, reservationID uuid.UUID) (Reservation, error) {
	reservation, ok := r.reservations[reservationID]
	if !ok || reservation.AccountID != accountID {
		return Reservation{}, ErrNotFound
	}
	return reservation, nil
}

// ListStaleReservations mirrors the production predicates: non-terminal status,
// and untouched since the cutoff on both timestamps.
func (r *repoStub) ListStaleReservations(_ context.Context, olderThan time.Time, limit int) ([]Reservation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var stale []Reservation
	for _, reservation := range r.reservations {
		if reservation.Status != ReservationStatusActive && reservation.Status != ReservationStatusExpanded {
			continue
		}
		if !reservation.CreatedAt.Before(olderThan) || !reservation.UpdatedAt.Before(olderThan) {
			continue
		}
		stale = append(stale, reservation)
		if limit > 0 && len(stale) == limit {
			break
		}
	}
	return stale, nil
}

func (r *repoStub) ExpandReservation(_ context.Context, accountID, reservationID uuid.UUID, additionalCredits int64, reason string) (Reservation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	reservation, err := r.getLocked(accountID, reservationID)
	if err != nil {
		return Reservation{}, err
	}

	reservation.ReservedCredits += additionalCredits
	reservation.Status = ReservationStatusExpanded
	reservation.UpdatedAt = time.Now().UTC()
	r.reservations[reservationID] = reservation
	return reservation, nil
}

func (r *repoStub) FinalizeReservation(_ context.Context, accountID, reservationID uuid.UUID, consumedCredits, releasedCredits int64, terminalUsageConfirmed bool, status ReservationStatus, reason string) (Reservation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	reservation, err := r.getLocked(accountID, reservationID)
	if err != nil {
		return Reservation{}, err
	}

	reservation.ConsumedCredits = consumedCredits
	reservation.ReleasedCredits = releasedCredits
	reservation.TerminalUsageConfirmed = terminalUsageConfirmed
	reservation.Status = status
	reservation.UpdatedAt = time.Now().UTC()
	r.reservations[reservationID] = reservation
	return reservation, nil
}

func (r *repoStub) ReleaseReservation(_ context.Context, accountID, reservationID uuid.UUID, releasedCredits int64, reason string) (Reservation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	reservation, err := r.getLocked(accountID, reservationID)
	if err != nil {
		return Reservation{}, err
	}
	if reservation.Status == ReservationStatusReleased {
		return reservation, nil
	}

	reservation.ReleasedCredits = releasedCredits
	reservation.Status = ReservationStatusReleased
	reservation.UpdatedAt = time.Now().UTC()
	r.reservations[reservationID] = reservation
	r.releaseEventCounts[reservationID]++
	return reservation, nil
}

func (r *repoStub) CreateReconciliationJob(_ context.Context, reservationID, requestAttemptID uuid.UUID, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reconciliationJobs = append(r.reconciliationJobs, reconciliationJob{
		reservationID:    reservationID,
		requestAttemptID: requestAttemptID,
		reason:           reason,
	})
	return nil
}

func TestCreateReservationStrictPolicyRejectsOverBalance(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{balance: ledger.BalanceSummary{AvailableCredits: 50}}
	usageSvc := &usageStub{}
	svc := NewService(repo, ledgerSvc, usageSvc)

	_, err := svc.CreateReservation(context.Background(), CreateReservationInput{
		AccountID:        uuid.New(),
		RequestID:        "req_strict",
		AttemptNumber:    1,
		Endpoint:         "/v1/responses",
		ModelAlias:       "hive-fast",
		EstimatedCredits: 60,
		PolicyMode:       PolicyModeStrict,
	})
	if err == nil {
		t.Fatal("expected strict policy to reject over-balance reservation")
	}

	var policyErr *PolicyError
	if !errors.As(err, &policyErr) {
		t.Fatalf("expected PolicyError, got %T", err)
	}
	if len(repo.reservations) != 0 {
		t.Fatalf("expected no reservation to be stored, got %d", len(repo.reservations))
	}
}

func TestCreateReservationAllowsTemporaryOverageWithinBuffer(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{balance: ledger.BalanceSummary{AvailableCredits: 100}}
	usageSvc := &usageStub{}
	svc := NewService(repo, ledgerSvc, usageSvc)

	reservation, err := svc.CreateReservation(context.Background(), CreateReservationInput{
		AccountID:        uuid.New(),
		RequestID:        "req_overage",
		AttemptNumber:    2,
		Endpoint:         "/v1/responses",
		ModelAlias:       "hive-fast",
		EstimatedCredits: 10100,
		PolicyMode:       PolicyModeTemporaryOverage,
		CustomerTags:     map[string]any{"project": "demo"},
	})
	if err != nil {
		t.Fatalf("CreateReservation returned error: %v", err)
	}

	if reservation.Status != ReservationStatusActive {
		t.Fatalf("expected active reservation, got %s", reservation.Status)
	}
	// The hold rides with the row now (issue #918), so it is recorded by the
	// repository rather than posted through the ledger service afterwards.
	if len(repo.holds) != 1 || repo.holds[0].Credits != 10100 {
		t.Fatalf("expected one 10100-credit hold written with the reservation, got %#v", repo.holds)
	}
	if len(ledgerSvc.reserveCalls) != 0 {
		t.Fatalf("hold must not be posted in a second transaction, got %#v", ledgerSvc.reserveCalls)
	}
	if len(usageSvc.startCalls) != 1 {
		t.Fatalf("expected one usage start call, got %d", len(usageSvc.startCalls))
	}
	if len(usageSvc.eventCalls) != 1 || usageSvc.eventCalls[0].EventType != usage.UsageEventReservationCreated {
		t.Fatalf("expected one reservation_created usage event, got %#v", usageSvc.eventCalls)
	}
}

func TestFinalizeReservationCreatesChargeAndRelease(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{}
	usageSvc := &usageStub{}
	svc := NewService(repo, ledgerSvc, usageSvc)

	accountID := uuid.New()
	attemptID := uuid.New()
	reservationID := uuid.New()
	repo.reservations[reservationID] = Reservation{
		ID:               reservationID,
		AccountID:        accountID,
		RequestAttemptID: attemptID,
		ReservationKey:   "req_final:1",
		PolicyMode:       PolicyModeStrict,
		Status:           ReservationStatusActive,
		ReservedCredits:  100,
	}

	reservation, err := svc.FinalizeReservation(context.Background(), FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservationID,
		ActualCredits:          70,
		TerminalUsageConfirmed: true,
		Status:                 string(usage.AttemptStatusCompleted),
	})
	if err != nil {
		t.Fatalf("FinalizeReservation returned error: %v", err)
	}

	if reservation.Status != ReservationStatusFinalized {
		t.Fatalf("expected finalized reservation, got %s", reservation.Status)
	}
	if reservation.ConsumedCredits != 70 {
		t.Fatalf("expected consumed credits 70, got %d", reservation.ConsumedCredits)
	}
	if reservation.ReleasedCredits != 30 {
		t.Fatalf("expected released credits 30, got %d", reservation.ReleasedCredits)
	}
	if len(ledgerSvc.chargeCalls) != 1 || ledgerSvc.chargeCalls[0].credits != 70 {
		t.Fatalf("expected one 70-credit charge, got %#v", ledgerSvc.chargeCalls)
	}
	// The ledger lifts the whole 100-credit hold, not the 30 left unused: the
	// 70 the charge captured has to leave the reserved bucket too, or available
	// balance carries it forever (issue #616).
	if len(ledgerSvc.releaseCalls) != 1 || ledgerSvc.releaseCalls[0].credits != 100 {
		t.Fatalf("expected one 100-credit hold lift, got %#v", ledgerSvc.releaseCalls)
	}
	if len(usageSvc.statusCalls) != 1 || usageSvc.statusCalls[0].status != usage.AttemptStatusCompleted {
		t.Fatalf("expected completed attempt status update, got %#v", usageSvc.statusCalls)
	}
}

// TestFinalizeReservationClampsChargeToReservedHold is the regression guard
// for issue #602: an edge-api token estimate that runs far ahead of the
// reserved hold (a huge request body disconnected after one output token)
// must never charge more than what was reserved. finalizeLocked is the
// single chokepoint every settlement path (sync + streaming, all endpoints)
// routes through, so this test asserts the clamp there rather than at any
// one caller.
func TestFinalizeReservationClampsChargeToReservedHold(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{}
	usageSvc := &usageStub{}
	svc := NewService(repo, ledgerSvc, usageSvc)

	accountID := uuid.New()
	attemptID := uuid.New()
	reservationID := uuid.New()
	repo.reservations[reservationID] = Reservation{
		ID:               reservationID,
		AccountID:        accountID,
		RequestAttemptID: attemptID,
		ReservationKey:   "req_overcharge:1",
		PolicyMode:       PolicyModeStrict,
		Status:           ReservationStatusActive,
		ReservedCredits:  10000,
	}

	// A deliberately enormous estimate, far beyond the 10000-credit hold.
	reservation, err := svc.FinalizeReservation(context.Background(), FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservationID,
		ActualCredits:          2_600_000,
		TerminalUsageConfirmed: false,
		Status:                 string(usage.AttemptStatusInterrupted),
	})
	if err != nil {
		t.Fatalf("FinalizeReservation returned error: %v", err)
	}

	if reservation.ConsumedCredits != 10000 {
		t.Fatalf("expected consumed credits clamped to the 10000 hold, got %d", reservation.ConsumedCredits)
	}
	if reservation.ReleasedCredits != 0 {
		t.Fatalf("expected zero released credits when the estimate exhausts the hold, got %d", reservation.ReleasedCredits)
	}
	if len(ledgerSvc.chargeCalls) != 1 || ledgerSvc.chargeCalls[0].credits != 10000 {
		t.Fatalf("expected the ledger charge clamped to 10000, got %#v", ledgerSvc.chargeCalls)
	}
	// Nothing is left unused, so the row releases 0, but the ledger still has
	// to lift the 10000 hold the charge just captured. Before issue #616 this
	// path posted no release at all, which stranded the entire hold.
	if len(ledgerSvc.releaseCalls) != 1 || ledgerSvc.releaseCalls[0].credits != 10000 {
		t.Fatalf("expected the full 10000 hold lifted even though the clamped charge consumed all of it, got %#v", ledgerSvc.releaseCalls)
	}
}

// TestFinalizeReservationConfirmedUsageAboveHoldIsNotClamped is the
// regression guard for the follow-up review finding on the clamp above: the
// clamp must only ever apply to an unconfirmed estimate. When upstream
// confirms real usage above the reservation's flat, never-expanded hold
// (long context, RAG, coding-agent traffic routinely exceed 10000 tokens),
// the true amount must still be charged in full -- the hold is an
// authorization floor, not a ceiling on a confirmed fact. Silently capping
// a confirmed charge would undercharge with no reconciliation job, since
// TerminalUsageConfirmed=true skips reconciliation entirely.
func TestFinalizeReservationConfirmedUsageAboveHoldIsNotClamped(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{}
	usageSvc := &usageStub{}
	svc := NewService(repo, ledgerSvc, usageSvc)

	accountID := uuid.New()
	attemptID := uuid.New()
	reservationID := uuid.New()
	repo.reservations[reservationID] = Reservation{
		ID:               reservationID,
		AccountID:        accountID,
		RequestAttemptID: attemptID,
		ReservationKey:   "req_confirmed_overage:1",
		PolicyMode:       PolicyModeStrict,
		Status:           ReservationStatusActive,
		ReservedCredits:  10000,
	}

	// Upstream confirmed 15000 real tokens against a flat 10000 hold.
	reservation, err := svc.FinalizeReservation(context.Background(), FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservationID,
		ActualCredits:          15000,
		TerminalUsageConfirmed: true,
		Status:                 string(usage.AttemptStatusCompleted),
	})
	if err != nil {
		t.Fatalf("FinalizeReservation returned error: %v", err)
	}

	if reservation.ConsumedCredits != 15000 {
		t.Fatalf("expected confirmed usage billed in full unclamped, got %d", reservation.ConsumedCredits)
	}
	if reservation.ReleasedCredits != 0 {
		t.Fatalf("expected zero released credits on a confirmed overage, got %d", reservation.ReleasedCredits)
	}
	if len(ledgerSvc.chargeCalls) != 1 || ledgerSvc.chargeCalls[0].credits != 15000 {
		t.Fatalf("expected the ledger charge for the true 15000, got %#v", ledgerSvc.chargeCalls)
	}
	// The charge exceeds the hold, so nothing is unused and the row releases 0,
	// but the 10000-credit authorization is still outstanding until the ledger
	// lifts it (issue #616).
	if len(ledgerSvc.releaseCalls) != 1 || ledgerSvc.releaseCalls[0].credits != 10000 {
		t.Fatalf("expected the full 10000 hold lifted on a confirmed overage, got %#v", ledgerSvc.releaseCalls)
	}
}

func TestFinalizeReservationMarksAmbiguousStreamForReconciliation(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{}
	usageSvc := &usageStub{}
	svc := NewService(repo, ledgerSvc, usageSvc)

	accountID := uuid.New()
	attemptID := uuid.New()
	reservationID := uuid.New()
	repo.reservations[reservationID] = Reservation{
		ID:               reservationID,
		AccountID:        accountID,
		RequestAttemptID: attemptID,
		ReservationKey:   "req_ambiguous:1",
		PolicyMode:       PolicyModeStrict,
		Status:           ReservationStatusActive,
		ReservedCredits:  90,
	}

	reservation, err := svc.FinalizeReservation(context.Background(), FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservationID,
		ActualCredits:          40,
		TerminalUsageConfirmed: false,
		Status:                 string(usage.AttemptStatusInterrupted),
	})
	if err != nil {
		t.Fatalf("FinalizeReservation returned error: %v", err)
	}

	if reservation.Status != ReservationStatusNeedsReconciliation {
		t.Fatalf("expected needs_reconciliation reservation, got %s", reservation.Status)
	}
	if len(repo.reconciliationJobs) != 1 {
		t.Fatalf("expected one reconciliation job, got %d", len(repo.reconciliationJobs))
	}
	if len(ledgerSvc.chargeCalls) != 1 || ledgerSvc.chargeCalls[0].credits != 40 {
		t.Fatalf("expected one 40-credit charge, got %#v", ledgerSvc.chargeCalls)
	}
	// 90 held, 40 charged: the ledger lifts all 90, the row records 50 unused
	// (issue #616).
	if len(ledgerSvc.releaseCalls) != 1 || ledgerSvc.releaseCalls[0].credits != 90 {
		t.Fatalf("expected one 90-credit hold lift, got %#v", ledgerSvc.releaseCalls)
	}
	if len(usageSvc.statusCalls) != 1 || usageSvc.statusCalls[0].status != usage.AttemptStatusInterrupted {
		t.Fatalf("expected interrupted status update, got %#v", usageSvc.statusCalls)
	}
}

func TestReleaseReservationWritesReleaseEventsOnlyOnce(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{}
	usageSvc := &usageStub{}
	svc := NewService(repo, ledgerSvc, usageSvc)

	accountID := uuid.New()
	attemptID := uuid.New()
	reservationID := uuid.New()
	repo.reservations[reservationID] = Reservation{
		ID:               reservationID,
		AccountID:        accountID,
		RequestAttemptID: attemptID,
		ReservationKey:   "req_release:1",
		PolicyMode:       PolicyModeStrict,
		Status:           ReservationStatusActive,
		ReservedCredits:  75,
	}

	if _, err := svc.ReleaseReservation(context.Background(), ReleaseReservationInput{
		AccountID:     accountID,
		ReservationID: reservationID,
		Reason:        "cancelled",
	}); err != nil {
		t.Fatalf("first ReleaseReservation returned error: %v", err)
	}

	if _, err := svc.ReleaseReservation(context.Background(), ReleaseReservationInput{
		AccountID:     accountID,
		ReservationID: reservationID,
		Reason:        "cancelled",
	}); err != nil {
		t.Fatalf("second ReleaseReservation returned error: %v", err)
	}

	if len(ledgerSvc.releaseCalls) != 1 {
		t.Fatalf("expected one ledger release call, got %d", len(ledgerSvc.releaseCalls))
	}
	if repo.releaseEventCounts[reservationID] != 1 {
		t.Fatalf("expected one stored release event, got %d", repo.releaseEventCounts[reservationID])
	}
}

func TestFinalizeReservationRecordsCompletedEventAndUpdatesAPIKeyUsage(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{balance: ledger.BalanceSummary{AvailableCredits: 500}}
	usageSvc := &usageStub{}
	apiKeySvc := &apiKeyStub{}
	svc := NewService(repo, ledgerSvc, usageSvc, apiKeySvc)

	accountID := uuid.New()
	apiKeyID := uuid.New()

	reservation, err := svc.CreateReservation(context.Background(), CreateReservationInput{
		AccountID:        accountID,
		RequestID:        "req_attributed_finalize",
		AttemptNumber:    1,
		APIKeyID:         &apiKeyID,
		Endpoint:         "/v1/responses",
		ModelAlias:       "hive-fast",
		EstimatedCredits: 70,
		PolicyMode:       PolicyModeStrict,
	})
	if err != nil {
		t.Fatalf("CreateReservation returned error: %v", err)
	}

	_, err = svc.FinalizeReservation(context.Background(), FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservation.ID,
		ActualCredits:          70,
		TerminalUsageConfirmed: true,
		Status:                 string(usage.AttemptStatusCompleted),
	})
	if err != nil {
		t.Fatalf("FinalizeReservation returned error: %v", err)
	}

	if len(usageSvc.eventCalls) != 2 {
		t.Fatalf("expected reservation_created and completed events, got %#v", usageSvc.eventCalls)
	}
	finalizeEvent := usageSvc.eventCalls[len(usageSvc.eventCalls)-1]
	if finalizeEvent.EventType != usage.UsageEventCompleted {
		t.Fatalf("expected completed usage event, got %s", finalizeEvent.EventType)
	}
	if finalizeEvent.APIKeyID == nil || *finalizeEvent.APIKeyID != apiKeyID {
		t.Fatalf("expected completed event to carry API key ID %s, got %#v", apiKeyID, finalizeEvent.APIKeyID)
	}
	if len(apiKeySvc.finalizationCalls) != 1 || apiKeySvc.finalizationCalls[0].apiKeyID != apiKeyID {
		t.Fatalf("expected usage finalization recorded for API key %s, got %#v", apiKeyID, apiKeySvc.finalizationCalls)
	}
	if len(apiKeySvc.lastUsedCalls) != 1 || apiKeySvc.lastUsedCalls[0].apiKeyID != apiKeyID {
		t.Fatalf("expected last_used_at update for API key %s, got %#v", apiKeyID, apiKeySvc.lastUsedCalls)
	}
}

func TestFinalizeReservationUpdatesBudgetWindowAndUsageRollup(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{balance: ledger.BalanceSummary{AvailableCredits: 500}}
	usageSvc := &usageStub{}
	apiKeyID := uuid.New()
	apiKeySvc := &apiKeyStub{
		budgetKindByKey: map[uuid.UUID]string{apiKeyID: "lifetime"},
	}
	svc := NewService(repo, ledgerSvc, usageSvc, apiKeySvc)

	accountID := uuid.New()
	reservation, err := svc.CreateReservation(context.Background(), CreateReservationInput{
		AccountID:        accountID,
		RequestID:        "req_budget_rollup",
		AttemptNumber:    1,
		APIKeyID:         &apiKeyID,
		Endpoint:         "/v1/responses",
		ModelAlias:       "hive-fast",
		EstimatedCredits: 100,
		PolicyMode:       PolicyModeStrict,
	})
	if err != nil {
		t.Fatalf("CreateReservation returned error: %v", err)
	}

	_, err = svc.FinalizeReservation(context.Background(), FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservation.ID,
		ActualCredits:          70,
		TerminalUsageConfirmed: true,
		Status:                 string(usage.AttemptStatusCompleted),
	})
	if err != nil {
		t.Fatalf("FinalizeReservation returned error: %v", err)
	}

	if len(apiKeySvc.deltaCalls) != 2 {
		t.Fatalf("expected 2 budget window updates, got %#v", apiKeySvc.deltaCalls)
	}
	if apiKeySvc.deltaCalls[0].reservedDelta != 100 || apiKeySvc.deltaCalls[0].consumedDelta != 0 {
		t.Fatalf("expected reservation create to add 100 reserved credits, got %#v", apiKeySvc.deltaCalls[0])
	}
	if apiKeySvc.deltaCalls[1].reservedDelta != -100 || apiKeySvc.deltaCalls[1].consumedDelta != 70 {
		t.Fatalf("expected finalize to clear open reserve and add consumed credits, got %#v", apiKeySvc.deltaCalls[1])
	}
	if len(apiKeySvc.finalizationCalls) != 1 {
		t.Fatalf("expected one usage rollup update, got %#v", apiKeySvc.finalizationCalls)
	}
	if apiKeySvc.finalizationCalls[0].modelAlias != "hive-fast" || apiKeySvc.finalizationCalls[0].consumedCredits != 70 {
		t.Fatalf("expected finalization rollup to record model and consumed credits, got %#v", apiKeySvc.finalizationCalls[0])
	}
}

func TestBudgetProjectionCountsOpenReservations(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{balance: ledger.BalanceSummary{AvailableCredits: 500}}
	usageSvc := &usageStub{}
	apiKeyID := uuid.New()
	apiKeySvc := &apiKeyStub{
		budgetKindByKey: map[uuid.UUID]string{apiKeyID: "lifetime"},
	}
	svc := NewService(repo, ledgerSvc, usageSvc, apiKeySvc)

	accountID := uuid.New()
	reservation, err := svc.CreateReservation(context.Background(), CreateReservationInput{
		AccountID:        accountID,
		RequestID:        "req_projection",
		AttemptNumber:    1,
		APIKeyID:         &apiKeyID,
		Endpoint:         "/v1/responses",
		ModelAlias:       "hive-fast",
		EstimatedCredits: 80,
		PolicyMode:       PolicyModeStrict,
	})
	if err != nil {
		t.Fatalf("CreateReservation returned error: %v", err)
	}

	if _, err := svc.ExpandReservation(context.Background(), ExpandReservationInput{
		AccountID:         accountID,
		ReservationID:     reservation.ID,
		AdditionalCredits: 20,
	}); err != nil {
		t.Fatalf("ExpandReservation returned error: %v", err)
	}

	projected := projectedBudgetWindow(apiKeySvc.deltaCalls, apiKeyID, "lifetime")
	if projected.reserved != 100 || projected.consumed != 0 {
		t.Fatalf("expected open reservations to project 100 reserved credits, got %#v", projected)
	}

	if _, err := svc.FinalizeReservation(context.Background(), FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservation.ID,
		ActualCredits:          100,
		TerminalUsageConfirmed: true,
		Status:                 string(usage.AttemptStatusCompleted),
	}); err != nil {
		t.Fatalf("FinalizeReservation returned error: %v", err)
	}

	projected = projectedBudgetWindow(apiKeySvc.deltaCalls, apiKeyID, "lifetime")
	if projected.reserved != 0 || projected.consumed != 100 {
		t.Fatalf("expected finalized reservation to leave no open reserve and 100 consumed credits, got %#v", projected)
	}
}

func TestFinalizeReservationUsesConfiguredBudgetWindowKind(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{balance: ledger.BalanceSummary{AvailableCredits: 500}}
	usageSvc := &usageStub{}
	apiKeyID := uuid.New()
	apiKeySvc := &apiKeyStub{
		budgetKindByKey: map[uuid.UUID]string{apiKeyID: "monthly"},
	}
	svc := NewService(repo, ledgerSvc, usageSvc, apiKeySvc)

	accountID := uuid.New()
	reservation, err := svc.CreateReservation(context.Background(), CreateReservationInput{
		AccountID:        accountID,
		RequestID:        "req_monthly_window",
		AttemptNumber:    1,
		APIKeyID:         &apiKeyID,
		Endpoint:         "/v1/responses",
		ModelAlias:       "hive-fast",
		EstimatedCredits: 55,
		PolicyMode:       PolicyModeStrict,
	})
	if err != nil {
		t.Fatalf("CreateReservation returned error: %v", err)
	}

	if _, err := svc.FinalizeReservation(context.Background(), FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservation.ID,
		ActualCredits:          55,
		TerminalUsageConfirmed: true,
		Status:                 string(usage.AttemptStatusCompleted),
	}); err != nil {
		t.Fatalf("FinalizeReservation returned error: %v", err)
	}

	if len(apiKeySvc.deltaCalls) != 2 {
		t.Fatalf("expected 2 budget window updates, got %#v", apiKeySvc.deltaCalls)
	}
	for _, call := range apiKeySvc.deltaCalls {
		if call.budgetKind != "monthly" {
			t.Fatalf("expected budget updates to resolve monthly window, got %#v", apiKeySvc.deltaCalls)
		}
	}
}

type budgetProjection struct {
	reserved int64
	consumed int64
}

func projectedBudgetWindow(calls []apiKeyDeltaCall, apiKeyID uuid.UUID, budgetKind string) budgetProjection {
	var projection budgetProjection
	for _, call := range calls {
		if call.apiKeyID != apiKeyID || call.budgetKind != budgetKind {
			continue
		}
		projection.reserved += call.reservedDelta
		projection.consumed += call.consumedDelta
	}
	return projection
}

// TestFinalizeReservationTwiceChargesOnce is the structural half of "exactly
// one settled entry per served request" (#746). usage_events carries no unique
// index on (request_id, event_type), so nothing in the schema stops a second
// settlement row; what stops it is this replay guard, and a retried or
// duplicated finalize is the ordinary way it gets exercised.
func TestFinalizeReservationTwiceChargesOnce(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{}
	usageSvc := &usageStub{}
	svc := NewService(repo, ledgerSvc, usageSvc)

	accountID := uuid.New()
	reservationID := uuid.New()
	repo.reservations[reservationID] = Reservation{
		ID:               reservationID,
		AccountID:        accountID,
		RequestAttemptID: uuid.New(),
		ReservationKey:   "req_replay:1",
		PolicyMode:       PolicyModeStrict,
		Status:           ReservationStatusActive,
		ReservedCredits:  10000,
	}

	input := FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservationID,
		ActualCredits:          22,
		TerminalUsageConfirmed: true,
		Status:                 string(usage.AttemptStatusCompleted),
		InputTokens:            111,
		OutputTokens:           70,
	}
	if _, err := svc.FinalizeReservation(context.Background(), input); err != nil {
		t.Fatalf("first FinalizeReservation returned error: %v", err)
	}
	if _, err := svc.FinalizeReservation(context.Background(), input); err != nil {
		t.Fatalf("second FinalizeReservation returned error: %v", err)
	}

	if len(ledgerSvc.chargeCalls) != 1 {
		t.Fatalf("expected exactly one charge across two finalize calls, got %#v", ledgerSvc.chargeCalls)
	}
	if ledgerSvc.chargeCalls[0].credits != 22 {
		t.Fatalf("expected a 22-credit charge, got %d", ledgerSvc.chargeCalls[0].credits)
	}

	var settlements []usage.RecordEventInput
	for _, event := range usageSvc.eventCalls {
		if event.EventType == usage.UsageEventCompleted {
			settlements = append(settlements, event)
		}
	}
	if len(settlements) != 1 {
		t.Fatalf("expected exactly one settlement usage event, got %d", len(settlements))
	}
	// The settled row carries the metered quantities alongside the negative
	// credit delta, so the console reads real tokens rather than zero (#856).
	if settlements[0].InputTokens != 111 || settlements[0].OutputTokens != 70 {
		t.Fatalf("settlement event lost the metered tokens: %+v", settlements[0])
	}
	if settlements[0].HiveCreditDelta != -22 {
		t.Fatalf("expected a -22 credit delta, got %d", settlements[0].HiveCreditDelta)
	}
}

// TestFinalizeClampsNegativeProviderTokenCounts: the token counts on a
// settlement originate in a provider response, so they are external input on a
// money path. CreditsForTokens already clamps them before pricing, so a
// negative count cannot reduce a charge, but an unclamped count reaching
// usage_events would sit in the console's analytics beside a non-negative
// credit delta and make a SUM over that column understate consumption.
func TestFinalizeClampsNegativeProviderTokenCounts(t *testing.T) {
	repo := newRepoStub()
	ledgerSvc := &ledgerStub{}
	usageSvc := &usageStub{}
	svc := NewService(repo, ledgerSvc, usageSvc)

	accountID := uuid.New()
	reservationID := uuid.New()
	repo.reservations[reservationID] = Reservation{
		ID:               reservationID,
		AccountID:        accountID,
		RequestAttemptID: uuid.New(),
		ReservationKey:   "req_negative:1",
		PolicyMode:       PolicyModeStrict,
		Status:           ReservationStatusActive,
		ReservedCredits:  10000,
	}

	if _, err := svc.FinalizeReservation(context.Background(), FinalizeReservationInput{
		AccountID:              accountID,
		ReservationID:          reservationID,
		ActualCredits:          22,
		TerminalUsageConfirmed: true,
		Status:                 string(usage.AttemptStatusCompleted),
		InputTokens:            -111,
		OutputTokens:           -70,
	}); err != nil {
		t.Fatalf("FinalizeReservation returned error: %v", err)
	}

	// The charge itself, not only the number reported next to it: a regression
	// that skipped ChargeUsage entirely would still record -22 on the event.
	if len(ledgerSvc.chargeCalls) != 1 {
		t.Fatalf("expected exactly one ledger charge, got %#v", ledgerSvc.chargeCalls)
	}
	if ledgerSvc.chargeCalls[0].credits != 22 {
		t.Fatalf("expected a 22-credit charge, got %d", ledgerSvc.chargeCalls[0].credits)
	}

	var settlements []usage.RecordEventInput
	for _, event := range usageSvc.eventCalls {
		if event.EventType == usage.UsageEventCompleted {
			settlements = append(settlements, event)
		}
	}
	if len(settlements) != 1 {
		t.Fatalf("expected exactly one settlement usage event, got %d", len(settlements))
	}
	if settlements[0].InputTokens != 0 || settlements[0].OutputTokens != 0 {
		t.Fatalf("negative provider token counts reached usage_events: %+v", settlements[0])
	}
	if settlements[0].HiveCreditDelta != -22 {
		t.Fatalf("expected a -22 credit delta, got %d", settlements[0].HiveCreditDelta)
	}
}
