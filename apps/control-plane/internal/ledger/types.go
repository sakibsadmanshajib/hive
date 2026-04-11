package ledger

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type EntryType string

const (
	EntryTypeGrant              EntryType = "grant"
	EntryTypeAdjustment         EntryType = "adjustment"
	EntryTypeReservationHold    EntryType = "reservation_hold"
	EntryTypeReservationRelease EntryType = "reservation_release"
	EntryTypeUsageCharge        EntryType = "usage_charge"
	EntryTypeRefund             EntryType = "refund"
)

type LedgerEntry struct {
	ID             uuid.UUID      `json:"id"`
	AccountID      uuid.UUID      `json:"account_id,omitempty"`
	EntryType      EntryType      `json:"entry_type"`
	CreditsDelta   int64          `json:"credits_delta"`
	IdempotencyKey string         `json:"idempotency_key"`
	RequestID      string         `json:"request_id,omitempty"`
	AttemptID      *uuid.UUID     `json:"attempt_id,omitempty"`
	ReservationID  *uuid.UUID     `json:"reservation_id,omitempty"`
	Metadata       map[string]any `json:"metadata"`
	CreatedAt      time.Time      `json:"created_at"`
}

type BalanceSummary struct {
	PostedCredits    int64 `json:"posted_credits"`
	ReservedCredits  int64 `json:"reserved_credits"`
	AvailableCredits int64 `json:"available_credits"`
}

type PostEntryInput struct {
	EntryType      EntryType
	CreditsDelta   int64
	IdempotencyKey string
	RequestID      string
	AttemptID      *uuid.UUID
	ReservationID  *uuid.UUID
	Metadata       map[string]any
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

func AsValidationError(err error, target **ValidationError) bool {
	return errors.As(err, target)
}

var ErrNotFound = errors.New("ledger: not found")

// InvoiceRow represents a payment invoice record.
type InvoiceRow struct {
	ID              uuid.UUID      `json:"id"`
	AccountID       uuid.UUID      `json:"account_id"`
	PaymentIntentID uuid.UUID      `json:"payment_intent_id"`
	InvoiceNumber   string         `json:"invoice_number"`
	Status          string         `json:"status"`
	Credits         int64          `json:"credits"`
	AmountUSD       int64          `json:"amount_usd"`
	AmountLocal     int64          `json:"amount_local"`
	LocalCurrency   string         `json:"local_currency"`
	TaxTreatment    string         `json:"tax_treatment"`
	Rail            string         `json:"rail"`
	LineItems       []map[string]any `json:"line_items"`
	CreatedAt       time.Time      `json:"created_at"`
}

// ListEntriesFilter supports cursor-based pagination for ledger entry listing.
type ListEntriesFilter struct {
	AccountID uuid.UUID
	Cursor    *uuid.UUID
	Limit     int
	EntryType *EntryType
}
