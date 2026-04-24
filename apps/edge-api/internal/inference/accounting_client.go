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
)

// AccountingClient calls the control-plane internal accounting and usage endpoints.
type AccountingClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewAccountingClient creates a new AccountingClient.
func NewAccountingClient(baseURL string) *AccountingClient {
	return &AccountingClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
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
type ReservationResult struct {
	ID               string `json:"id"`
	AccountID        string `json:"account_id"`
	Status           string `json:"status"`
	EstimatedCredits int64  `json:"estimated_credits"`
}

// FinalizeReservationInput is the request body for finalizing a reservation.
type FinalizeReservationInput struct {
	AccountID              string `json:"account_id"`
	ReservationID          string `json:"reservation_id"`
	ActualCredits          int64  `json:"actual_credits"`
	TerminalUsageConfirmed bool   `json:"terminal_usage_confirmed"`
	Status                 string `json:"status"`
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

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	if output != nil {
		if err := json.Unmarshal(respBody, output); err != nil {
			return fmt.Errorf("decode: %w", err)
		}
	}

	return nil
}
