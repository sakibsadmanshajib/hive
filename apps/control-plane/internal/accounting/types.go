package accounting

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type PolicyMode string

const (
	PolicyModeStrict            PolicyMode = "strict"
	PolicyModeTemporaryOverage  PolicyMode = "temporary_overage"
	temporaryOverageBuffer int64 = 10000
)

type ReservationStatus string

const (
	ReservationStatusActive              ReservationStatus = "active"
	ReservationStatusExpanded            ReservationStatus = "expanded"
	ReservationStatusFinalized           ReservationStatus = "finalized"
	ReservationStatusReleased            ReservationStatus = "released"
	ReservationStatusNeedsReconciliation ReservationStatus = "needs_reconciliation"
)

type Reservation struct {
	ID                     uuid.UUID         `json:"id"`
	AccountID              uuid.UUID         `json:"account_id,omitempty"`
	RequestAttemptID       uuid.UUID         `json:"request_attempt_id"`
	ReservationKey         string            `json:"reservation_key"`
	RequestID              string            `json:"request_id"`
	AttemptNumber          int               `json:"attempt_number"`
	Endpoint               string            `json:"endpoint"`
	ModelAlias             string            `json:"model_alias"`
	CustomerTags           map[string]any    `json:"customer_tags"`
	PolicyMode             PolicyMode        `json:"policy_mode"`
	Status                 ReservationStatus `json:"status"`
	ReservedCredits        int64             `json:"reserved_credits"`
	ConsumedCredits        int64             `json:"consumed_credits"`
	ReleasedCredits        int64             `json:"released_credits"`
	TerminalUsageConfirmed bool              `json:"terminal_usage_confirmed"`
	CreatedAt              time.Time         `json:"created_at"`
	UpdatedAt              time.Time         `json:"updated_at"`
}

type CreateReservationInput struct {
	AccountID        uuid.UUID
	RequestID        string
	AttemptNumber    int
	Endpoint         string
	ModelAlias       string
	EstimatedCredits int64
	PolicyMode       PolicyMode
	CustomerTags     map[string]any
}

type ExpandReservationInput struct {
	AccountID         uuid.UUID
	ReservationID     uuid.UUID
	AdditionalCredits int64
}

type FinalizeReservationInput struct {
	AccountID              uuid.UUID
	ReservationID          uuid.UUID
	ActualCredits          int64
	TerminalUsageConfirmed bool
	Status                 string
}

type ReleaseReservationInput struct {
	AccountID     uuid.UUID
	ReservationID uuid.UUID
	Reason        string
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

type PolicyError struct {
	Message string
}

func (e *PolicyError) Error() string {
	return e.Message
}

func AsValidationError(err error, target **ValidationError) bool {
	return errors.As(err, target)
}

var ErrNotFound = errors.New("accounting: not found")
