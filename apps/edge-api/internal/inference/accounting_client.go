package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/cpauth"
)

// AccountingClient calls the control-plane internal accounting and usage endpoints.
type AccountingClient struct {
	baseURL    string
	httpClient *http.Client
}

// accountingTimeout bounds a single control-plane accounting call. A
// reservation is six sequential round trips to Postgres, so it takes 4-8s
// when the database is a region away from the gateway (measured on the demo
// box against Supabase us-east-1). The former 5s budget cut that call off
// mid-flight: control-plane still committed the reservation while edge-api
// saw a timeout, so /v1/audio/* answered 402 and the credit hold leaked.
//
// A var, not a const, so tests can shrink it to exercise the
// deadline-exhaustion path in milliseconds instead of real seconds. Same
// reason the audio and images packages made their releaseTimeout a var in
// PR #650.
var accountingTimeout = 30 * time.Second

// NewAccountingClient creates a new AccountingClient.
func NewAccountingClient(baseURL string) *AccountingClient {
	return &AccountingClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: accountingTimeout},
	}
}

// StatusError reports a non-2xx response from a control-plane accounting or
// usage call. Callers match on StatusCode to tell a business rejection (409,
// credit policy) from an infrastructure fault (5xx, timeout, auth).
type StatusError struct {
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("status %d: %s", e.StatusCode, e.Body)
}

// --- Reservation types ---

// CreateReservationInput is the request body for creating a reservation.
type CreateReservationInput struct {
	AccountID        string         `json:"account_id"`
	RequestID        string         `json:"request_id"`
	AttemptNumber    int            `json:"attempt_number"`
	APIKeyID         string         `json:"api_key_id"`
	Endpoint         string         `json:"endpoint"`
	ModelAlias       string         `json:"model_alias"`
	EstimatedCredits int64          `json:"estimated_credits"`
	PolicyMode       string         `json:"policy_mode"`
	CustomerTags     map[string]any `json:"customer_tags,omitempty"`
}

// ReservationResult is the response from reservation endpoints.
//
// EstimatedCredits is a field the control plane does NOT send. Its response
// body is accounting.Reservation, which publishes the hold as `reserved_credits`
// (apps/control-plane/internal/accounting/types.go). Nothing read
// EstimatedCredits before variable pricing did, so the mismatch was invisible;
// the first reader of it got a silent 0. Both keys are decoded here rather than
// renaming the old one, so a caller that still reads EstimatedCredits keeps its
// existing (zero) behaviour instead of changing meaning underneath it, and Held
// is the accessor anything sizing a charge must use.
type ReservationResult struct {
	ID               string `json:"id"`
	AccountID        string `json:"account_id"`
	Status           string `json:"status"`
	EstimatedCredits int64  `json:"estimated_credits"`
	ReservedCredits  int64  `json:"reserved_credits"`
}

// Held is the size of the hold the control plane actually recorded. Prefer the
// key it really sends; fall back to the legacy one so a stubbed or older
// responder still works.
func (r ReservationResult) Held() int64 {
	if r.ReservedCredits > 0 {
		return r.ReservedCredits
	}
	return r.EstimatedCredits
}

// FinalizeReservationInput is the request body for finalizing a reservation.
type FinalizeReservationInput struct {
	AccountID              string `json:"account_id"`
	ReservationID          string `json:"reservation_id"`
	ActualCredits          int64  `json:"actual_credits"`
	TerminalUsageConfirmed bool   `json:"terminal_usage_confirmed"`
	Status                 string `json:"status"`
	// InputTokens and OutputTokens are the metered quantities behind
	// ActualCredits. Control-plane writes them onto the settlement usage
	// event, so one row carries both what was consumed and what it cost, and
	// the console's token counters read a real figure instead of zero (#856).
	// Optional: a caller that omits them settles exactly as before.
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`
	// CacheReadTokens and CacheWriteTokens are the prompt's cache components
	// behind ActualCredits (#1174). Control-plane forwards them into the
	// api_key_usage_rollups write, so the rollup stops accumulating zeroes in
	// exactly the token classes that dominate agent traffic. Optional like
	// their siblings: a caller that omits them settles exactly as before.
	CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
}

// ReleaseReservationInput is the request body for releasing a reservation.
type ReleaseReservationInput struct {
	AccountID     string `json:"account_id"`
	ReservationID string `json:"reservation_id"`
	Reason        string `json:"reason"`
}

// --- Usage types ---

// StartAttemptInput is the request body for starting a usage attempt.
type StartAttemptInput struct {
	AccountID     string         `json:"account_id"`
	RequestID     string         `json:"request_id"`
	AttemptNumber int            `json:"attempt_number"`
	Endpoint      string         `json:"endpoint"`
	ModelAlias    string         `json:"model_alias"`
	Status        string         `json:"status"`
	APIKeyID      string         `json:"api_key_id"`
	CustomerTags  map[string]any `json:"customer_tags,omitempty"`
}

// AttemptResult is the response from starting an attempt.
type AttemptResult struct {
	ID        string `json:"id"`
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

// RecordEventInput is the request body for recording a usage event.
type RecordEventInput struct {
	AccountID        string         `json:"account_id"`
	RequestAttemptID string         `json:"request_attempt_id"`
	APIKeyID         string         `json:"api_key_id"`
	RequestID        string         `json:"request_id"`
	EventType        string         `json:"event_type"`
	Endpoint         string         `json:"endpoint"`
	ModelAlias       string         `json:"model_alias"`
	Status           string         `json:"status"`
	InputTokens      int64          `json:"input_tokens"`
	OutputTokens     int64          `json:"output_tokens"`
	CacheReadTokens  int64          `json:"cache_read_tokens"`
	CacheWriteTokens int64          `json:"cache_write_tokens"`
	HiveCreditDelta  int64          `json:"hive_credit_delta"`
	CustomerTags     map[string]any `json:"customer_tags,omitempty"`
	ErrorCode        string         `json:"error_code,omitempty"`
	ErrorType        string         `json:"error_type,omitempty"`
}

// --- Reservation methods ---

// CreateReservation calls POST /internal/accounting/reservations.
func (c *AccountingClient) CreateReservation(ctx context.Context, input CreateReservationInput) (ReservationResult, error) {
	var result ReservationResult
	if err := c.post(ctx, "/internal/accounting/reservations", input, &result); err != nil {
		return ReservationResult{}, fmt.Errorf("accounting: create reservation: %w", err)
	}
	return result, nil
}

// FinalizeReservation calls POST /internal/accounting/reservations/finalize.
func (c *AccountingClient) FinalizeReservation(ctx context.Context, input FinalizeReservationInput) error {
	return c.post(ctx, "/internal/accounting/reservations/finalize", input, nil)
}

// ReleaseReservation calls POST /internal/accounting/reservations/release.
func (c *AccountingClient) ReleaseReservation(ctx context.Context, input ReleaseReservationInput) error {
	return c.post(ctx, "/internal/accounting/reservations/release", input, nil)
}

// --- Usage methods ---

// StartAttempt calls POST /internal/usage/attempts.
func (c *AccountingClient) StartAttempt(ctx context.Context, input StartAttemptInput) (AttemptResult, error) {
	var result AttemptResult
	if err := c.post(ctx, "/internal/usage/attempts", input, &result); err != nil {
		return AttemptResult{}, fmt.Errorf("accounting: start attempt: %w", err)
	}
	return result, nil
}

// RecordUsageEvent calls POST /internal/usage/events.
func (c *AccountingClient) RecordUsageEvent(ctx context.Context, input RecordEventInput) error {
	return c.post(ctx, "/internal/usage/events", input, nil)
}

// post is a helper that POSTs JSON to a path and optionally decodes the response.
func (c *AccountingClient) post(ctx context.Context, path string, input any, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	cpauth.SetHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &StatusError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(respBody))}
	}

	if output != nil {
		if err := json.Unmarshal(respBody, output); err != nil {
			return fmt.Errorf("decode: %w", err)
		}
	}

	return nil
}
