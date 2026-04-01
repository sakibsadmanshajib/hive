package accounting

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/ledger"
	"github.com/hivegpt/hive/apps/control-plane/internal/usage"
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
	ApplyReservationDelta(ctx context.Context, apiKeyID uuid.UUID, budgetKind string, reservedDelta int64, consumedDelta int64, at time.Time) error
	RecordUsageFinalization(ctx context.Context, apiKeyID uuid.UUID, modelAlias string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, consumedCredits int64, at time.Time) error
	MarkLastUsed(ctx context.Context, apiKeyID uuid.UUID, at time.Time) error
}

type Service struct {
	repo      Repository
	ledgerSvc ledgerService
	usageSvc  usageService
	apiKeySvc apiKeyService
}

func NewService(repo Repository, ledgerSvc ledgerService, usageSvc usageService, apiKeySvcs ...apiKeyService) *Service {
	var apiKeySvc apiKeyService
	if len(apiKeySvcs) > 0 {
		apiKeySvc = apiKeySvcs[0]
	}
	return &Service{repo: repo, ledgerSvc: ledgerSvc, usageSvc: usageSvc, apiKeySvc: apiKeySvc}
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

	balance, err := s.ledgerSvc.GetBalance(ctx, input.AccountID)
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: get balance: %w", err)
	}
	if err := enforcePolicy(input.PolicyMode, balance.AvailableCredits, input.EstimatedCredits); err != nil {
		return Reservation{}, err
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
		return Reservation{}, fmt.Errorf("accounting: start attempt: %w", err)
	}

	reservation, err := s.repo.CreateReservation(ctx, Reservation{
		ID:               uuid.New(),
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
	}, "reserved")
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: create reservation: %w", err)
	}

	if _, err := s.ledgerSvc.ReserveCredits(ctx, input.AccountID, input.RequestID, &attempt.ID, &reservation.ID, s.idempotencyKey(reservation.ID, "reserve"), input.EstimatedCredits, map[string]any{
		"policy_mode": input.PolicyMode,
		"endpoint":    input.Endpoint,
		"model_alias": input.ModelAlias,
	}); err != nil {
		return Reservation{}, fmt.Errorf("accounting: reserve credits: %w", err)
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
		return Reservation{}, fmt.Errorf("accounting: record reservation event: %w", err)
	}

	if input.APIKeyID != nil && s.apiKeySvc != nil {
		if err := s.apiKeySvc.ApplyReservationDelta(ctx, *input.APIKeyID, "lifetime", input.EstimatedCredits, 0, time.Now()); err != nil {
			return Reservation{}, fmt.Errorf("accounting: apply reservation delta: %w", err)
		}
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

	reservation, err := s.repo.GetReservation(ctx, input.AccountID, input.ReservationID)
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: get reservation: %w", err)
	}
	if reservation.Status == ReservationStatusFinalized || reservation.Status == ReservationStatusReleased {
		return Reservation{}, &PolicyError{Message: "reservation cannot be expanded after settlement"}
	}

	balance, err := s.ledgerSvc.GetBalance(ctx, input.AccountID)
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: get balance: %w", err)
	}

	currentHeld := remainingHeldCredits(reservation)
	if err := enforcePolicy(reservation.PolicyMode, balance.AvailableCredits+currentHeld, currentHeld+input.AdditionalCredits); err != nil {
		return Reservation{}, err
	}

	reservation, err = s.repo.ExpandReservation(ctx, input.AccountID, input.ReservationID, input.AdditionalCredits, "expanded")
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: expand reservation: %w", err)
	}

	if _, err := s.ledgerSvc.ReserveCredits(ctx, reservation.AccountID, reservation.RequestID, &reservation.RequestAttemptID, &reservation.ID, s.idempotencyKey(reservation.ID, fmt.Sprintf("expand-%d", input.AdditionalCredits)), input.AdditionalCredits, map[string]any{
		"endpoint":           reservation.Endpoint,
		"model_alias":        reservation.ModelAlias,
		"additional_credits": input.AdditionalCredits,
		"policy_mode":        reservation.PolicyMode,
	}); err != nil {
		return Reservation{}, fmt.Errorf("accounting: reserve expanded credits: %w", err)
	}

	if attempt, err := s.findAttempt(ctx, reservation.AccountID, reservation.RequestID, reservation.RequestAttemptID); err != nil {
		return Reservation{}, err
	} else if attempt != nil && attempt.APIKeyID != nil && s.apiKeySvc != nil {
		if err := s.apiKeySvc.ApplyReservationDelta(ctx, *attempt.APIKeyID, "lifetime", input.AdditionalCredits, 0, time.Now()); err != nil {
			return Reservation{}, fmt.Errorf("accounting: apply reservation delta: %w", err)
		}
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

	reservation, err := s.repo.GetReservation(ctx, input.AccountID, input.ReservationID)
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: get reservation: %w", err)
	}
	if reservation.Status == ReservationStatusFinalized || reservation.Status == ReservationStatusNeedsReconciliation {
		return reservation, nil
	}
	if reservation.Status == ReservationStatusReleased {
		return Reservation{}, &PolicyError{Message: "released reservations cannot be finalized"}
	}

	releaseCredits := releasableCredits(reservation, input.ActualCredits)

	if input.ActualCredits > 0 {
		if _, err := s.ledgerSvc.ChargeUsage(ctx, reservation.AccountID, reservation.RequestID, &reservation.RequestAttemptID, &reservation.ID, s.idempotencyKey(reservation.ID, fmt.Sprintf("charge-%d", input.ActualCredits)), input.ActualCredits, map[string]any{
			"endpoint":           reservation.Endpoint,
			"model_alias":        reservation.ModelAlias,
			"terminal_confirmed": input.TerminalUsageConfirmed,
		}); err != nil {
			return Reservation{}, fmt.Errorf("accounting: charge usage: %w", err)
		}
	}

	if releaseCredits > 0 {
		if _, err := s.ledgerSvc.ReleaseReservedCredits(ctx, reservation.AccountID, reservation.RequestID, &reservation.RequestAttemptID, &reservation.ID, s.idempotencyKey(reservation.ID, fmt.Sprintf("release-%d", releaseCredits)), releaseCredits, map[string]any{
			"endpoint":           reservation.Endpoint,
			"model_alias":        reservation.ModelAlias,
			"terminal_confirmed": input.TerminalUsageConfirmed,
		}); err != nil {
			return Reservation{}, fmt.Errorf("accounting: release reserved credits: %w", err)
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

	reservation, err = s.repo.FinalizeReservation(ctx, input.AccountID, input.ReservationID, input.ActualCredits, releaseCredits, input.TerminalUsageConfirmed, nextStatus, reason)
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
		HiveCreditDelta:  -input.ActualCredits,
		CustomerTags:     reservation.CustomerTags,
		InternalMetadata: map[string]any{
			"reservation_id":           reservation.ID.String(),
			"released_credits":         releaseCredits,
			"terminal_usage_confirmed": input.TerminalUsageConfirmed,
		},
	}); err != nil {
		return Reservation{}, fmt.Errorf("accounting: record finalize event: %w", err)
	}

	if attempt != nil && attempt.APIKeyID != nil && s.apiKeySvc != nil {
		at := time.Now().UTC()
		if err := s.apiKeySvc.ApplyReservationDelta(ctx, *attempt.APIKeyID, "lifetime", -releaseCredits, input.ActualCredits, at); err != nil {
			return Reservation{}, fmt.Errorf("accounting: apply reservation delta: %w", err)
		}
		if err := s.apiKeySvc.RecordUsageFinalization(ctx, *attempt.APIKeyID, attempt.ModelAlias, 0, 0, 0, 0, input.ActualCredits, at); err != nil {
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

	reservation, err := s.repo.GetReservation(ctx, input.AccountID, input.ReservationID)
	if err != nil {
		return Reservation{}, fmt.Errorf("accounting: get reservation: %w", err)
	}
	if reservation.Status == ReservationStatusReleased {
		return reservation, nil
	}
	if reservation.Status == ReservationStatusFinalized {
		return Reservation{}, &PolicyError{Message: "finalized reservations cannot be released"}
	}

	releaseCredits := remainingHeldCredits(reservation)
	if releaseCredits > 0 {
		if _, err := s.ledgerSvc.ReleaseReservedCredits(ctx, reservation.AccountID, reservation.RequestID, &reservation.RequestAttemptID, &reservation.ID, s.idempotencyKey(reservation.ID, "release"), releaseCredits, map[string]any{
			"endpoint":    reservation.Endpoint,
			"model_alias": reservation.ModelAlias,
			"reason":      input.Reason,
		}); err != nil {
			return Reservation{}, fmt.Errorf("accounting: release reserved credits: %w", err)
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

	if _, err := s.usageSvc.RecordEvent(ctx, usage.RecordEventInput{
		AccountID:        reservation.AccountID,
		RequestAttemptID: reservation.RequestAttemptID,
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

	if releaseCredits > 0 && s.apiKeySvc != nil {
		attempt, err := s.findAttempt(ctx, reservation.AccountID, reservation.RequestID, reservation.RequestAttemptID)
		if err != nil {
			return Reservation{}, err
		}
		if attempt != nil && attempt.APIKeyID != nil {
			if err := s.apiKeySvc.ApplyReservationDelta(ctx, *attempt.APIKeyID, "lifetime", -releaseCredits, 0, time.Now()); err != nil {
				return Reservation{}, fmt.Errorf("accounting: apply reservation delta: %w", err)
			}
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
