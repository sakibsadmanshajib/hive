package usage

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type AttemptStatus string

const (
	AttemptStatusAccepted    AttemptStatus = "accepted"
	AttemptStatusDispatching AttemptStatus = "dispatching"
	AttemptStatusStreaming   AttemptStatus = "streaming"
	AttemptStatusCompleted   AttemptStatus = "completed"
	AttemptStatusFailed      AttemptStatus = "failed"
	AttemptStatusCancelled   AttemptStatus = "cancelled"
	AttemptStatusInterrupted AttemptStatus = "interrupted"
)

type UsageEventType string

const (
	UsageEventAccepted           UsageEventType = "accepted"
	UsageEventReservationCreated UsageEventType = "reservation_created"
	UsageEventStreamUpdate       UsageEventType = "stream_update"
	UsageEventCompleted          UsageEventType = "completed"
	UsageEventReleased           UsageEventType = "released"
	UsageEventRefunded           UsageEventType = "refunded"
	UsageEventError              UsageEventType = "error"
	UsageEventReconciled         UsageEventType = "reconciled"
)

type RequestAttempt struct {
	ID               uuid.UUID      `json:"id"`
	AccountID        uuid.UUID      `json:"account_id,omitempty"`
	RequestID        string         `json:"request_id"`
	AttemptNumber    int            `json:"attempt_number"`
	Endpoint         string         `json:"endpoint"`
	ModelAlias       string         `json:"model_alias"`
	Status           AttemptStatus  `json:"status"`
	UserID           *uuid.UUID     `json:"user_id,omitempty"`
	TeamID           *uuid.UUID     `json:"team_id,omitempty"`
	ServiceAccountID *uuid.UUID     `json:"service_account_id,omitempty"`
	APIKeyID         *uuid.UUID     `json:"api_key_id,omitempty"`
	CustomerTags     map[string]any `json:"customer_tags"`
	StartedAt        time.Time      `json:"started_at"`
	CompletedAt      *time.Time     `json:"completed_at,omitempty"`
}

type UsageEvent struct {
	ID                uuid.UUID      `json:"id"`
	AccountID         uuid.UUID      `json:"account_id,omitempty"`
	RequestAttemptID  uuid.UUID      `json:"request_attempt_id"`
	APIKeyID          *uuid.UUID     `json:"api_key_id,omitempty"`
	RequestID         string         `json:"request_id"`
	EventType         UsageEventType `json:"event_type"`
	Endpoint          string         `json:"endpoint"`
	ModelAlias        string         `json:"model_alias"`
	Status            string         `json:"status"`
	InputTokens       int64          `json:"input_tokens"`
	OutputTokens      int64          `json:"output_tokens"`
	CacheReadTokens   int64          `json:"cache_read_tokens"`
	CacheWriteTokens  int64          `json:"cache_write_tokens"`
	HiveCreditDelta   int64          `json:"hive_credit_delta"`
	ProviderRequestID string         `json:"provider_request_id,omitempty"`
	InternalMetadata  map[string]any `json:"internal_metadata,omitempty"`
	CustomerTags      map[string]any `json:"customer_tags"`
	ErrorCode         string         `json:"error_code,omitempty"`
	ErrorType         string         `json:"error_type,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
}

type StartAttemptInput struct {
	AccountID        uuid.UUID
	RequestID        string
	AttemptNumber    int
	Endpoint         string
	ModelAlias       string
	Status           AttemptStatus
	UserID           *uuid.UUID
	TeamID           *uuid.UUID
	ServiceAccountID *uuid.UUID
	APIKeyID         *uuid.UUID
	CustomerTags     map[string]any
}

type RecordEventInput struct {
	AccountID         uuid.UUID
	RequestAttemptID  uuid.UUID
	APIKeyID          *uuid.UUID
	RequestID         string
	EventType         UsageEventType
	Endpoint          string
	ModelAlias        string
	Status            string
	InputTokens       int64
	OutputTokens      int64
	CacheReadTokens   int64
	CacheWriteTokens  int64
	HiveCreditDelta   int64
	ProviderRequestID string
	InternalMetadata  map[string]any
	CustomerTags      map[string]any
	ErrorCode         string
	ErrorType         string
}

type ListEventsFilter struct {
	AccountID uuid.UUID
	RequestID string
	Limit     int
}

// UsageSummaryRow holds aggregated usage data grouped by a dimension.
type UsageSummaryRow struct {
	GroupKey          string `json:"group_key"`
	TotalInputTokens  int64  `json:"total_input_tokens"`
	TotalOutputTokens int64  `json:"total_output_tokens"`
	TotalCreditsSpent int64  `json:"total_credits_spent"`
	RequestCount      int64  `json:"request_count"`
}

// SpendSummaryRow holds aggregated spend data grouped by a dimension.
type SpendSummaryRow struct {
	GroupKey    string `json:"group_key"`
	TotalCredits int64  `json:"total_credits"`
	EntryCount  int64  `json:"entry_count"`
}

// ErrorSummaryRow holds aggregated error rate data grouped by a dimension.
type ErrorSummaryRow struct {
	GroupKey      string  `json:"group_key"`
	ErrorCount    int64   `json:"error_count"`
	TotalRequests int64   `json:"total_requests"`
	ErrorRate     float64 `json:"error_rate"`
}

// AnalyticsFilter specifies the time window and grouping dimension for analytics queries.
type AnalyticsFilter struct {
	AccountID uuid.UUID
	GroupBy   string // "model", "api_key", "endpoint"
	From      time.Time
	To        time.Time
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
