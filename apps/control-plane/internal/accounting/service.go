package accounting

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usage"
)

type ledgerService interface {
	GetBalance(ctx context.Context, accountID uuid.UUID) (ledger.BalanceSummary, error)
	ReserveCredits(ctx context.Context, accountID uuid.UUID, requestID string, attemptID, reservationID *uuid.UUID, idempotencyKey string, credits int64, metadata map[string]any) (ledger.LedgerEntry, error)
	ReleaseReservedCredits(ctx context.Context, accountID uuid.UUID, requestID string, attemptID, reservationID *uuid.UUID, idempotencyKey string, credits int64, metadata map[string]any) (ledger.LedgerEntry, error)
	ChargeUsage(ctx context.Context, accountID uuid.UUID, requestID string, attemptID, reservationID *uuid.UUID, idempotencyKey string, credits int64, metadata map[string]any) (ledger.LedgerEntry, error)
	RefundCredits(ctx context.Context, accountID uuid.UUID, requestID string, attemptID, reservationID *uuid.UUID, idempotencyKey string, credits int64, metadata map[string]any) (ledger.LedgerEntry, error)
}

type usageService interface {
	StartAttempt(ctx context.Context, input usage.StartAttemptInput) (usage.RequestAttempt, error)
	UpdateAttemptStatus(ctx context.Context, attemptID uuid.UUID, status usage.AttemptStatus, completedAt *time.Time) error
	RecordEvent(ctx context.Context, input usage.RecordEventInput) (usage.UsageEvent, error)
	ListAttempts(ctx context.Context, accountID uuid.UUID, requestID string, limit int) ([]usage.RequestAttempt, error)
}

type apiKeyService interface {
	ApplyReservationDelta(ctx context.Context, apiKeyID uuid.UUID, reservedDelta int64, consumedDelta int64, at time.Time) error
	RecordUsageFinalization(ctx context.Context, apiKeyID uuid.UUID, modelAlias string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, consumedCredits int64, at time.Time) error
	MarkLastUsed(ctx context.Context, apiKeyID uuid.UUID, at time.Time) error
}

type Service struct {
	repo      Repository
	ledgerSvc ledgerService
	usageSvc  usageService
	apiKeySvc apiKeyService
	locker    AccountLocker
}

func NewService(repo Repository, ledgerSvc ledgerService, usageSvc usageService, apiKeySvcs ...apiKeyService) *Service {
	var apiKeySvc apiKeyService
	if len(apiKeySvcs) > 0 {
		apiKeySvc = apiKeySvcs[0]
	}
	// Default to in-process serialization. Multi-instance deployments override
	// this with a cross-process locker via WithAccountLocker (see main.go).
	return &Service{repo: repo, ledgerSvc: ledgerSvc, usageSvc: usageSvc, apiKeySvc: apiKeySvc, locker: NewProcessAccountLocker()}
}

// WithAccountLocker overrides the per-account reservation locker. Returns the
// service for chaining. Used in production wiring to install the Postgres
// advisory locker so the credit-reservation critical section is serialized
// across all control-plane instances (issue #106).
func (s *Service) WithAccountLocker(locker AccountLocker) *Service {
	if locker != nil {
		s.locker = locker
	}
	return s
}

func (s *Service) CreateReservation(ctx context.Context, input CreateReservationInput) (Reservation, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.Endpoint = strings.TrimSpace(input.Endpoint)
	input.ModelAlias = strings.TrimSpace(input.ModelAlias)
	input.CustomerTags = normalizeCustomerTags(input.CustomerTags)
	if input.PolicyMode == "" {
		input.PolicyMode = PolicyModeStrict
	}

	if err := validateCreateReservation(input); err != nil {
		return Reservation{}, err
	}

	// Serialize the balance read → policy check → reservation hold per account.
	// Concurrent requests for the same account would otherwise all observe the
	// same available balance and each reserve up to the full amount, allowing a
	// TOCTOU credit double-spend (issue #106).
	var reservation Reservation
	if lockErr := s.locker.WithAccountLock(ctx, input.AccountID, func(ctx context.Context) error {
		balance, err := s.ledgerSvc.GetBalance(ctx, input.AccountID)
		if err != nil {
			return fmt.Errorf("accounting: get balance: %w", err)
		}
		if err := enforcePolicy(input.PolicyMode, balance.AvailableCredits, input.EstimatedCredits); err != nil {
			return err
		}

		attempt, err := s.usageSvc.StartAttempt(ctx, usage.StartAttemptInput{
			AccountID:     input.AccountID,
			RequestID:     input.RequestID,
			AttemptNumber: input.AttemptNumber,
			APIKeyID:      input.APIKeyID,
			Endpoint:      input.Endpoint,
			ModelAlias:    input.ModelAlias,
			Status:        usage.AttemptStatusAccepted,
			CustomerTags:  input.CustomerTags,
		})
		if err != nil {
			return fmt.Errorf("accounting: start attempt: %w", err)
		}

		reservationID := uuid.New()

		// Row and hold, one transaction (issue #918). The hold used to be a
		// separate ledger call made right here, after the row had already
		// committed, so a hold that failed left a reservation claiming credits
		// the ledger never held, and the reaper later released that row against
		// nothing, crediting an account for credits never taken from it. The
		// repository writes both or neither now, so the invariant is the
		// database's to keep rather than this function's.
		reservation, err = s.repo.CreateReservation(ctx, Reservation{
			ID:               reservationID,
			AccountID:        input.AccountID,
			RequestAttemptID: attempt.ID,
			ReservationKey:   buildReservationKey(input.AccountID, input.RequestID, input.AttemptNumber),
			RequestID:        input.RequestID,
			AttemptNumber:    input.AttemptNumber,
			Endpoint:         input.Endpoint,
			ModelAlias:       input.ModelAlias,
			CustomerTags:     input.CustomerTags,
			PolicyMode:       input.PolicyMode,
			Status:           ReservationStatusActive,
			ReservedCredits:  input.EstimatedCredits,
		}, "reserved", ReservationHold{
			IdempotencyKey: s.idempotencyKey(reservationID, "reserve"),
			Credits:        input.EstimatedCredits,
			Metadata: map[string]any{
				"policy_mode": input.PolicyMode,
				"endpoint":    input.Endpoint,
				"model_alias": input.ModelAlias,
			},
		})
		if err != nil {
			return fmt.Errorf("accounting: create reservation: %w", err)
		}

		if _, err := s.usageSvc.RecordEvent(ctx, usage.RecordEventInput{
			AccountID:        input.AccountID,
			RequestAttemptID: attempt.ID,
			APIKeyID:         input.APIKeyID,
			RequestID:        input.RequestID,
			EventType:        usage.UsageEventReservationCreated,
			Endpoint:         input.Endpoint,
			ModelAlias:       input.ModelAlias,
			Status:           string(attempt.Status),
			CustomerTags:     input.CustomerTags,
			InternalMetadata: map[string]any{
				"reservation_id":    reservation.ID.String(),
				"reservation_key":   reservation.ReservationKey,
				"estimated_credits": input.EstimatedCredits,
				"policy_mode":       input.PolicyMode,
			},
		}); err != nil {
			return fmt.Errorf("accounting: record reservation event: %w", err)
		}

		if input.APIKeyID != nil && s.apiKeySvc != nil {
			if err := s.apiKeySvc.ApplyReservationDelta(ctx, *input.APIKeyID, input.EstimatedCredits, 0, time.Now().UTC()); err != nil {
				return fmt.Errorf("accounting: apply reservation delta: %w", err)
			}
		}

		return nil
	}); lockErr != nil {
		return Reservation{}, lockErr
	}

	return reservation, nil
}

func (s *Service) ExpandReservation(ctx context.Context, input ExpandReservationInput) (Reservation, error) {
	if input.ReservationID == uuid.Nil {
		return Reservation{}, &ValidationError{Field: "reservation_id", Message: "reservation_id is required"}
	}
	if input.AdditionalCredits <= 0 {
		return Reservation{}, &ValidationError{Field: "additional_credits", Message: "additional_credits must be greater than zero"}
	}

	// Same per-account serialization as CreateReservation: an expansion also
	// reads the balance before posting an additional hold, so concurrent
	// expansions (or a create racing an expand) must not double-spend (#106).
	var reservation Reservation
	if lockErr := s.locker.WithAccountLock(ctx, input.AccountID, func(ctx context.Context) error {
		var err error
		reservation, err = s.repo.GetReservation(ctx, input.AccountID, input.ReservationID)
		if err != nil {
			return fmt.Errorf("accounting: get reservation: %w", err)
		}
		if !reservationOpen(reservation.Status) {
			return &PolicyError{Message: "reservation cannot be expanded after settlement"}
		}

		balance, err := s.ledgerSvc.GetBalance(ctx, input.AccountID)
		if err != nil {
			return fmt.Errorf("accounting: get balance: %w", err)
		}

		currentHeld := remainingHeldCredits(reservation)
		if err := enforcePolicy(reservation.PolicyMode, balance.AvailableCredits+currentHeld, currentHeld+input.AdditionalCredits); err != nil {
			return err
		}

		// Expansion carries its hold in the same transaction as the row, for
		// the reason create does (issue #918). This path had the identical
		// defect and it is worse here, because settlement releases from the ROW:
		// a row raised to 2500 while the ledger still holds 500 releases 2500
		// against 500 ever taken, crediting the account 2000 it never paid.
		reservation, err = s.repo.ExpandReservation(ctx, input.AccountID, input.ReservationID, input.AdditionalCredits, "expanded", ReservationHold{
			IdempotencyKey: s.idempotencyKey(input.ReservationID, fmt.Sprintf("expand-%d", input.AdditionalCredits)),
			Credits:        input.AdditionalCredits,
			Metadata: map[string]any{
				"endpoint":           reservation.Endpoint,
				"model_alias":        reservation.ModelAlias,
				"additional_credits": input.AdditionalCredits,
				"policy_mode":        reservation.PolicyMode,
			},
		})
		if err != nil {
			return fmt.Errorf("accounting: expand reservation: %w", err)
		}

		if attempt, err := s.findAttempt(ctx, reservation.AccountID, reservation.RequestID, reservation.RequestAttemptID); err != nil {
			return err
		} else if attempt != nil && attempt.APIKeyID != nil && s.apiKeySvc != nil {
			if err := s.apiKeySvc.ApplyReservationDelta(ctx, *attempt.APIKeyID, input.AdditionalCredits, 0, time.Now().UTC()); err != nil {
				return fmt.Errorf("accounting: apply reservation delta: %w", err)
			}
		}

		return nil
	}); lockErr != nil {
		return Reservation{}, lockErr
	}

	return reservation, nil
}

func (s *Service) FinalizeReservation(ctx context.Context, input FinalizeReservationInput) (Reservation, error) {
	status, completedAt, err := parseAttemptStatus(input.Status)
	if err != nil {
		return Reservation{}, err
	}
	if input.ReservationID == uuid.Nil {
		return Reservation{}, &ValidationError{Field: "reservation_id", Message: "reservation_id is required"}
	}
	if input.ActualCredits < 0 {
		return Reservation{}, &ValidationError{Field: "actual_credits", Message: "actual_credits must not be negative"}
	}

	// Finalization posts charge/release ledger entries, which change the
	// account's available balance, so it must take the same per-account lock as
	// create/expand. Otherwise a concurrent reservation could read the
	// pre-charge balance and reserve credits that finalization then consumes,
	// overdrawing the account (issue #106).
	var reservation Reservation
	if lockErr := s.locker.WithAccountLock(ctx, input.AccountID, func(ctx context.Context) error {
		var err error
		reservation, err = s.finalizeLocked(ctx, input, status, completedAt)
		return err
	}); lockErr != nil {
		return Reservation{}, lockErr
	}
	return reservation, nil
}

func (s *Service) finalizeLocked(ctx context.Context, input FinalizeReservationInput, status usage.AttemptStatus, completedAt *time.Time) (Reservation, error) {
	reservation, err := s.repo.GetReservation(ctx, input.AccountID, input.ReservationID)
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: get reservation: %w", err)
	}
	// Already settled by a charge: replay the stored row rather than charging
	// twice. needs_reconciliation counts as settled here because the charge did
	// commit; only the estimate behind it is provisional.
	if reservation.Status == ReservationStatusFinalized || reservation.Status == ReservationStatusNeedsReconciliation {
		return reservation, nil
	}
	if !reservationOpen(reservation.Status) {
		return Reservation{}, &PolicyError{Message: string(reservation.Status) + " reservations cannot be finalized"}
	}

	heldCredits := remainingHeldCredits(reservation)

	// Hard invariant, estimates only: an UNCONFIRMED settlement must never
	// charge more than it reserved. input.ActualCredits there is an edge-api
	// token estimate that can run far ahead of the hold (e.g. a huge request
	// body disconnected after one output token, see issue #602).
	// finalizeLocked is the single chokepoint every settlement path routes
	// through (sync and streaming, all endpoints), so clamping here protects
	// the ledger even if a future estimator change reintroduces an overcount.
	//
	// The hold is an authorization floor, not a ceiling on truth: when
	// TerminalUsageConfirmed is true the upstream itself reported the token
	// count, so it must be billed in full even past the hold -- edge-api's
	// reservation is a flat, never-expanded estimate (DefaultHoldText for
	// chat/completions/responses, DefaultHoldEmbeddings for embeddings, in
	// the current credit unit; ExpandReservation exists but no caller ever
	// invokes it), so any request whose real usage legitimately exceeds that
	// floor (long context, RAG, coding-agent traffic) would otherwise be
	// silently undercharged with no
	// reconciliation job, since TerminalUsageConfirmed=true skips
	// reconciliation entirely. Clamping a confirmed fact was the bug the
	// PR #602 review caught in the previous version of this fix.
	actualCredits := input.ActualCredits
	if !input.TerminalUsageConfirmed && actualCredits > heldCredits {
		actualCredits = heldCredits
	}

	// unusedCredits is a fact about the RESERVATION: how much of the hold never
	// turned into spend. It is what the row records beside consumed_credits, and
	// on every path except a confirmed overage (where the charge legitimately
	// exceeds the hold and this is zero) the two sum to reserved_credits.
	unusedCredits := releasableCredits(reservation, actualCredits)

	if actualCredits > 0 {
		// The charge key still carries the amount. Flattening it to
		// "reservation:<id>:charge", the way issue #652 flattened the release
		// key, needs a migration normalizing the 1866 existing
		// "charge-<credits>" entries first (issue #663 is the same lesson for
		// the release key), because until they are normalized a retry after a
		// crossed deploy would post a SECOND charge under the new key shape
		// instead of deduplicating against the old one. Tracked separately;
		// capture-exactly-once rests meanwhile on the reservationOpen status
		// guard above plus the per-account lock, as it always has.
		if _, err := s.ledgerSvc.ChargeUsage(ctx, reservation.AccountID, reservation.RequestID, &reservation.RequestAttemptID, &reservation.ID, s.idempotencyKey(reservation.ID, fmt.Sprintf("charge-%d", actualCredits)), actualCredits, map[string]any{
			"endpoint":           reservation.Endpoint,
			"model_alias":        reservation.ModelAlias,
			"terminal_confirmed": input.TerminalUsageConfirmed,
		}); err != nil {
			return Reservation{}, fmt.Errorf("accounting: charge usage: %w", err)
		}
	}

	// The ledger releases the WHOLE outstanding hold, including the part the
	// charge just captured, not merely the unused remainder. A hold is an
	// authorization, and capture lifts it in full; the charge is what the
	// customer actually pays. Releasing only the remainder left `actual`
	// credits sitting in the ledger's reserved bucket forever, and since
	// GetBalance computes available as posted minus reserved, every settled
	// request cost the customer its price TWICE: once posted, once withheld
	// (issue #616, measured live 2026-08-16 -- 143642 credits held against
	// 1825 reservations that had already settled, one 18-credit completion
	// moving available by 36). The reaper cannot recover these, because it
	// only scans holds still in a non-terminal state.
	//
	// This is also what the API key counter has always done a few lines below:
	// ApplyReservationDelta unwinds -heldCredits, never -unusedCredits.
	//
	// Idempotency key is "release", not "release-<credits>" (issue #652):
	// releaseLocked below keys its own release the same way, and a key that
	// varies with the amount cannot deduplicate two release attempts of
	// DIFFERING amounts for the same reservation. Both paths now also release
	// the same quantity, remainingHeldCredits, so whichever arrives first
	// posts an entry the other would have posted identically.
	if heldCredits > 0 {
		entry, err := s.ledgerSvc.ReleaseReservedCredits(ctx, reservation.AccountID, reservation.RequestID, &reservation.RequestAttemptID, &reservation.ID, s.idempotencyKey(reservation.ID, "release"), heldCredits, map[string]any{
			"endpoint":           reservation.Endpoint,
			"model_alias":        reservation.ModelAlias,
			"terminal_confirmed": input.TerminalUsageConfirmed,
			"captured_credits":   actualCredits,
		})
		if err != nil {
			return Reservation{}, fmt.Errorf("accounting: release reserved credits: %w", err)
		}
		if err := assertReleasedInFull(reservation.ID, entry, heldCredits); err != nil {
			return Reservation{}, err
		}
	}

	nextStatus := ReservationStatusFinalized
	reason := "finalized"
	eventType := usage.UsageEventCompleted
	if !input.TerminalUsageConfirmed {
		nextStatus = ReservationStatusNeedsReconciliation
		reason = "needs_reconciliation"
		eventType = usage.UsageEventReconciled
	}

	reservation, err = s.repo.FinalizeReservation(ctx, input.AccountID, input.ReservationID, actualCredits, unusedCredits, input.TerminalUsageConfirmed, nextStatus, reason)
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: finalize reservation: %w", err)
	}

	if !input.TerminalUsageConfirmed {
		if err := s.repo.CreateReconciliationJob(ctx, reservation.ID, reservation.RequestAttemptID, "missing_terminal_usage"); err != nil {
			return Reservation{}, fmt.Errorf("accounting: create reconciliation job: %w", err)
		}
	}

	if err := s.usageSvc.UpdateAttemptStatus(ctx, reservation.RequestAttemptID, status, completedAt); err != nil {
		return Reservation{}, fmt.Errorf("accounting: update attempt status: %w", err)
	}

	attempt, err := s.findAttempt(ctx, reservation.AccountID, reservation.RequestID, reservation.RequestAttemptID)
	if err != nil {
		return Reservation{}, err
	}
	var apiKeyID *uuid.UUID
	if attempt != nil {
		apiKeyID = attempt.APIKeyID
	}

	if _, err := s.usageSvc.RecordEvent(ctx, usage.RecordEventInput{
		AccountID:        reservation.AccountID,
		RequestAttemptID: reservation.RequestAttemptID,
		APIKeyID:         apiKeyID,
		RequestID:        reservation.RequestID,
		EventType:        eventType,
		Endpoint:         reservation.Endpoint,
		ModelAlias:       reservation.ModelAlias,
		Status:           string(status),
		// The quantities behind the charge, when the caller measured them, so
		// this one row answers both what was consumed and what it cost. Spend
		// stays the negative credit delta; these are counts, never credits.
		//
		// Clamped, because these originate in a provider response and reach
		// here as external input. CreditsForTokens already clamps them before
		// pricing, so an unclamped count here would put a negative token total
		// into usage_events and the console's analytics beside a non-negative
		// credit delta, and a SUM over that column would silently understate
		// consumption.
		InputTokens:     max(input.InputTokens, 0),
		OutputTokens:    max(input.OutputTokens, 0),
		HiveCreditDelta: -actualCredits,
		CustomerTags:    reservation.CustomerTags,
		InternalMetadata: map[string]any{
			"reservation_id": reservation.ID.String(),
			// The unused part of the hold, matching the reservation row's
			// released_credits column. The ledger lifts more than this (the
			// whole authorization, captured part included), which is a
			// different question and deliberately not what analytics reads.
			"released_credits":         unusedCredits,
			"terminal_usage_confirmed": input.TerminalUsageConfirmed,
		},
	}); err != nil {
		return Reservation{}, fmt.Errorf("accounting: record finalize event: %w", err)
	}

	if attempt != nil && attempt.APIKeyID != nil && s.apiKeySvc != nil {
		at := time.Now().UTC()
		if err := s.apiKeySvc.ApplyReservationDelta(ctx, *attempt.APIKeyID, -heldCredits, actualCredits, at); err != nil {
			return Reservation{}, fmt.Errorf("accounting: apply reservation delta: %w", err)
		}
		if err := s.apiKeySvc.RecordUsageFinalization(ctx, *attempt.APIKeyID, attempt.ModelAlias, 0, 0, 0, 0, actualCredits, at); err != nil {
			return Reservation{}, fmt.Errorf("accounting: record usage finalization: %w", err)
		}
		if err := s.apiKeySvc.MarkLastUsed(ctx, *attempt.APIKeyID, at); err != nil {
			return Reservation{}, fmt.Errorf("accounting: mark last used: %w", err)
		}
	}

	return reservation, nil
}

func (s *Service) ReleaseReservation(ctx context.Context, input ReleaseReservationInput) (Reservation, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if input.ReservationID == uuid.Nil {
		return Reservation{}, &ValidationError{Field: "reservation_id", Message: "reservation_id is required"}
	}
	if input.Reason == "" {
		return Reservation{}, &ValidationError{Field: "reason", Message: "reason is required"}
	}

	// Release returns held credits to the available balance, so it serializes
	// under the same per-account lock as create/expand/finalize (issue #106).
	var reservation Reservation
	if lockErr := s.locker.WithAccountLock(ctx, input.AccountID, func(ctx context.Context) error {
		var err error
		reservation, err = s.releaseLocked(ctx, input)
		return err
	}); lockErr != nil {
		return Reservation{}, lockErr
	}
	return reservation, nil
}

func (s *Service) releaseLocked(ctx context.Context, input ReleaseReservationInput) (Reservation, error) {
	reservation, err := s.repo.GetReservation(ctx, input.AccountID, input.ReservationID)
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: get reservation: %w", err)
	}
	// Already released: this is a replay of THIS operation, not a second
	// settlement, so hand back the stored row.
	if reservation.Status == ReservationStatusReleased {
		return reservation, nil
	}
	// Anything else that is not an open hold has already reached a terminal
	// state, so releasing it again would be the second settlement of one
	// reservation (issue #672). needs_reconciliation is the case that used to
	// slip through: it is terminal in practice, since nothing consumes
	// credit_reconciliation_jobs (issue #600), so every such row was a standing
	// second-release target. Releasing one recomputed the release amount from a
	// hold that is already zero, which overwrote released_credits back to 0 and
	// destroyed the reconciliation marker, leaving a row that does not balance.
	if !reservationOpen(reservation.Status) {
		return Reservation{}, &PolicyError{Message: string(reservation.Status) + " reservations cannot be released"}
	}

	releaseCredits := remainingHeldCredits(reservation)
	if releaseCredits > 0 {
		// Same "release" key finalizeLocked's own release uses above (issue
		// #652): one reservation, one release key, whichever caller (edge-api
		// settlement fallback or the reaper) reaches it first. Both now compute
		// the same quantity, remainingHeldCredits, so whichever arrives second
		// deduplicates against an entry for the identical amount.
		entry, err := s.ledgerSvc.ReleaseReservedCredits(ctx, reservation.AccountID, reservation.RequestID, &reservation.RequestAttemptID, &reservation.ID, s.idempotencyKey(reservation.ID, "release"), releaseCredits, map[string]any{
			"endpoint":    reservation.Endpoint,
			"model_alias": reservation.ModelAlias,
			"reason":      input.Reason,
		})
		if err != nil {
			return Reservation{}, fmt.Errorf("accounting: release reserved credits: %w", err)
		}
		if err := assertReleasedInFull(reservation.ID, entry, releaseCredits); err != nil {
			return Reservation{}, err
		}
	}

	reservation, err = s.repo.ReleaseReservation(ctx, input.AccountID, input.ReservationID, releaseCredits, input.Reason)
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: release reservation: %w", err)
	}

	cancelled := usage.AttemptStatusCancelled
	now := time.Now().UTC()
	if err := s.usageSvc.UpdateAttemptStatus(ctx, reservation.RequestAttemptID, cancelled, &now); err != nil {
		return Reservation{}, fmt.Errorf("accounting: update attempt status: %w", err)
	}

	attempt, err := s.findAttempt(ctx, reservation.AccountID, reservation.RequestID, reservation.RequestAttemptID)
	if err != nil {
		return Reservation{}, err
	}
	var apiKeyID *uuid.UUID
	if attempt != nil {
		apiKeyID = attempt.APIKeyID
	}

	if _, err := s.usageSvc.RecordEvent(ctx, usage.RecordEventInput{
		AccountID:        reservation.AccountID,
		RequestAttemptID: reservation.RequestAttemptID,
		APIKeyID:         apiKeyID,
		RequestID:        reservation.RequestID,
		EventType:        usage.UsageEventReleased,
		Endpoint:         reservation.Endpoint,
		ModelAlias:       reservation.ModelAlias,
		Status:           string(cancelled),
		CustomerTags:     reservation.CustomerTags,
		InternalMetadata: map[string]any{
			"reservation_id":   reservation.ID.String(),
			"released_credits": releaseCredits,
			"reason":           input.Reason,
		},
	}); err != nil {
		return Reservation{}, fmt.Errorf("accounting: record release event: %w", err)
	}

	if releaseCredits > 0 && s.apiKeySvc != nil && attempt != nil && attempt.APIKeyID != nil {
		if err := s.apiKeySvc.ApplyReservationDelta(ctx, *attempt.APIKeyID, -releaseCredits, 0, now); err != nil {
			return Reservation{}, fmt.Errorf("accounting: apply reservation delta: %w", err)
		}
	}

	return reservation, nil
}

func (s *Service) findAttempt(ctx context.Context, accountID uuid.UUID, requestID string, attemptID uuid.UUID) (*usage.RequestAttempt, error) {
	attempts, err := s.usageSvc.ListAttempts(ctx, accountID, requestID, 50)
	if err != nil {
		return nil, fmt.Errorf("accounting: list attempts: %w", err)
	}
	for _, attempt := range attempts {
		if attempt.ID == attemptID {
			matched := attempt
			return &matched, nil
		}
	}
	return nil, nil
}

func validateCreateReservation(input CreateReservationInput) error {
	if input.AccountID == uuid.Nil {
		return &ValidationError{Field: "account_id", Message: "account_id is required"}
	}
	if input.RequestID == "" {
		return &ValidationError{Field: "request_id", Message: "request_id is required"}
	}
	if input.AttemptNumber <= 0 {
		return &ValidationError{Field: "attempt_number", Message: "attempt_number must be greater than zero"}
	}
	if input.Endpoint == "" {
		return &ValidationError{Field: "endpoint", Message: "endpoint is required"}
	}
	if input.ModelAlias == "" {
		return &ValidationError{Field: "model_alias", Message: "model_alias is required"}
	}
	if input.EstimatedCredits <= 0 {
		return &ValidationError{Field: "estimated_credits", Message: "estimated_credits must be greater than zero"}
	}
	switch input.PolicyMode {
	case PolicyModeStrict, PolicyModeTemporaryOverage:
		return nil
	default:
		return &ValidationError{Field: "policy_mode", Message: "policy_mode must be strict or temporary_overage"}
	}
}

func enforcePolicy(mode PolicyMode, availableCredits, requestedCredits int64) error {
	switch mode {
	case PolicyModeStrict:
		if requestedCredits > availableCredits {
			return &PolicyError{Message: "reservation exceeds available credits"}
		}
	case PolicyModeTemporaryOverage:
		if requestedCredits > availableCredits+temporaryOverageBuffer {
			return &PolicyError{Message: "reservation exceeds temporary overage buffer"}
		}
	default:
		return &ValidationError{Field: "policy_mode", Message: "policy_mode must be strict or temporary_overage"}
	}

	return nil
}

func parseAttemptStatus(raw string) (usage.AttemptStatus, *time.Time, error) {
	switch strings.TrimSpace(raw) {
	case string(usage.AttemptStatusStreaming):
		return usage.AttemptStatusStreaming, nil, nil
	case string(usage.AttemptStatusCompleted):
		now := time.Now().UTC()
		return usage.AttemptStatusCompleted, &now, nil
	case string(usage.AttemptStatusFailed):
		now := time.Now().UTC()
		return usage.AttemptStatusFailed, &now, nil
	case string(usage.AttemptStatusCancelled):
		now := time.Now().UTC()
		return usage.AttemptStatusCancelled, &now, nil
	case string(usage.AttemptStatusInterrupted):
		now := time.Now().UTC()
		return usage.AttemptStatusInterrupted, &now, nil
	default:
		return "", nil, &ValidationError{Field: "status", Message: "status must be streaming, completed, failed, cancelled, or interrupted"}
	}
}

// reservationOpen reports whether a reservation is still an open hold, meaning
// it has not yet reached a terminal state and may therefore be expanded,
// finalized, or released.
//
// This is an ALLOWLIST, not a blocklist, and deliberately so. Every guard here
// used to enumerate the settled statuses it refused, which is why
// needs_reconciliation slipped past the release guard the day it was added
// (issue #672) and past the expand guard as well: a status nobody remembered to
// add to the refusal list fell straight through into the permitted path. Listing
// the two open states instead means a newly added status fails CLOSED, refused
// by every settlement path until someone deliberately teaches this function
// about it. On the money path that is the correct default (D-034).
//
// Kept in sync with the CHECK constraint on public.credit_reservations.status
// (supabase/migrations/20260330_03_credit_reservations.sql) and with
// ListStaleReservations, whose reaper predicate selects the same two states.
func reservationOpen(status ReservationStatus) bool {
	switch status {
	case ReservationStatusActive, ReservationStatusExpanded:
		return true
	default:
		return false
	}
}

// assertReleasedInFull refuses to treat a reservation as settled when the
// ledger's release entry does not cover the hold this call meant to lift.
//
// PostEntry deduplicates on (account_id, entry_type, idempotency_key) and hands
// back the FIRST entry, so a release attempt that follows an earlier one for a
// SMALLER amount returns quietly with the hold only partly lifted. Settlement
// would then mark the row terminal, and the remainder would be stranded past
// the reaper, which only scans non-terminal holds: exactly the shape of issue
// #616 this change exists to end, reintroduced silently. The reachable sequence
// is a settlement that posted the old partial release and then failed before
// updating the row, with the retry landing on code that releases in full.
//
// Failing here leaves the reservation open, so it stays in the reaper's
// candidate scan and is reported on every pass instead of a wrong number being
// written once and disappearing. An unrecognized settlement fact is fatal on
// the money path (D-034).
//
// The comparison is against a POSITIVE amount because a release is stored
// positive: ledger.ReleaseReservedCredits posts +credits while ReserveCredits
// posts -credits, which is what makes the two cancel in GetBalance's sum. The
// live magnitude test would fail on its first settlement if that were inverted.
//
// A reservation that trips this cannot self-heal: its charge is already
// committed, and both settlement paths recompute the same amount and hit the
// same deduplicated entry. That is deliberate, since the alternatives are
// writing a number the ledger contradicts or silently dropping credits, but it
// needs an operator, so the error is typed rather than a bare string for
// alerting to key on. No reconciliation job is written: nothing drains that
// table today (issue #600) and the reaper would insert a fresh row every 15
// minutes for the same stuck reservation.
type SettlementDivergenceError struct {
	ReservationID uuid.UUID
	PostedCredits int64
	WantCredits   int64
}

func (e *SettlementDivergenceError) Error() string {
	return fmt.Sprintf("accounting: reservation %s already carries a %d-credit release, refusing to settle against a %d-credit hold", e.ReservationID, e.PostedCredits, e.WantCredits)
}

func assertReleasedInFull(reservationID uuid.UUID, entry ledger.LedgerEntry, want int64) error {
	if entry.CreditsDelta == want {
		return nil
	}
	return &SettlementDivergenceError{ReservationID: reservationID, PostedCredits: entry.CreditsDelta, WantCredits: want}
}

func remainingHeldCredits(reservation Reservation) int64 {
	remaining := reservation.ReservedCredits - reservation.ConsumedCredits - reservation.ReleasedCredits
	if remaining < 0 {
		return 0
	}
	return remaining
}

func releasableCredits(reservation Reservation, actualCredits int64) int64 {
	release := remainingHeldCredits(reservation) - actualCredits
	if release < 0 {
		return 0
	}
	return release
}

func buildReservationKey(accountID uuid.UUID, requestID string, attemptNumber int) string {
	return fmt.Sprintf("%s:%s:%d", accountID.String(), requestID, attemptNumber)
}

func normalizeCustomerTags(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	return input
}

func (s *Service) idempotencyKey(reservationID uuid.UUID, action string) string {
	return fmt.Sprintf("reservation:%s:%s", reservationID.String(), action)
}
