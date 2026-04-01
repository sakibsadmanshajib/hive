package accounting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/ledger"
	"github.com/hivegpt/hive/apps/control-plane/internal/usage"
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
	deltaCalls        []apiKeyDeltaCall
	finalizationCalls []apiKeyUsageCall
	lastUsedCalls     []struct {
		apiKeyID uuid.UUID
		at       time.Time
	}
}

func (a *apiKeyStub) ApplyReservationDelta(_ context.Context, apiKeyID uuid.UUID, budgetKind string, reservedDelta int64, consumedDelta int64, at time.Time) error {
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

type repoStub struct {
	reservations       map[uuid.UUID]Reservation
	reconciliationJobs []reconciliationJob
	releaseEventCounts map[uuid.UUID]int
}

func newRepoStub() *repoStub {
	return &repoStub{
		reservations:       make(map[uuid.UUID]Reservation),
		releaseEventCounts: make(map[uuid.UUID]int),
	}
}

func (r *repoStub) CreateReservation(_ context.Context, reservation Reservation, reason string) (Reservation, error) {
	now := time.Now().UTC()
	reservation.CreatedAt = now
	reservation.UpdatedAt = now
	r.reservations[reservation.ID] = reservation
	return reservation, nil
}

func (r *repoStub) GetReservation(_ context.Context, accountID, reservationID uuid.UUID) (Reservation, error) {
	reservation, ok := r.reservations[reservationID]
	if !ok || reservation.AccountID != accountID {
		return Reservation{}, ErrNotFound
	}
	return reservation, nil
}

func (r *repoStub) ExpandReservation(_ context.Context, accountID, reservationID uuid.UUID, additionalCredits int64, reason string) (Reservation, error) {
	reservation, err := r.GetReservation(context.Background(), accountID, reservationID)
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
	reservation, err := r.GetReservation(context.Background(), accountID, reservationID)
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
	reservation, err := r.GetReservation(context.Background(), accountID, reservationID)
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
	if len(ledgerSvc.reserveCalls) != 1 {
		t.Fatalf("expected one reserve ledger call, got %d", len(ledgerSvc.reserveCalls))
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
	if len(ledgerSvc.releaseCalls) != 1 || ledgerSvc.releaseCalls[0].credits != 30 {
		t.Fatalf("expected one 30-credit release, got %#v", ledgerSvc.releaseCalls)
	}
	if len(usageSvc.statusCalls) != 1 || usageSvc.statusCalls[0].status != usage.AttemptStatusCompleted {
		t.Fatalf("expected completed attempt status update, got %#v", usageSvc.statusCalls)
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
	if len(ledgerSvc.releaseCalls) != 1 || ledgerSvc.releaseCalls[0].credits != 50 {
		t.Fatalf("expected one 50-credit release, got %#v", ledgerSvc.releaseCalls)
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
