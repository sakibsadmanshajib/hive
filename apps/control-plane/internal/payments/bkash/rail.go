package bkash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/hivegpt/hive/apps/control-plane/internal/payments"
)

// Rail implements the payments.PaymentRail interface for bKash Tokenized Checkout.
type Rail struct {
	httpClient *http.Client
	baseURL    string
	appKey     string
	appSecret  string
	username   string
	password   string
}

// NewRail constructs a bKash Rail with the given HTTP client and credentials.
func NewRail(httpClient *http.Client, baseURL, appKey, appSecret, username, password string) *Rail {
	return &Rail{
		httpClient: httpClient,
		baseURL:    baseURL,
		appKey:     appKey,
		appSecret:  appSecret,
		username:   username,
		password:   password,
	}
}

// RailName returns the rail identifier for bKash.
func (r *Rail) RailName() payments.Rail {
	return payments.RailBkash
}

// grantToken calls the bKash grant-token endpoint and returns a fresh id_token.
// A new token is always fetched; tokens are never cached across sessions.
func (r *Rail) grantToken(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	body, err := json.Marshal(map[string]string{
		"app_key":    r.appKey,
		"app_secret": r.appSecret,
	})
	if err != nil {
		return "", fmt.Errorf("bkash: marshal grant token body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.baseURL+"/tokenized/checkout/token/grant", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("bkash: create grant token request: %w", err)
	}
	req.Header.Set("username", r.username)
	req.Header.Set("password", r.password)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("bkash: grant token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bkash: grant token response status: %d", resp.StatusCode)
	}

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("bkash: read grant token response: %w", err)
	}

	var result struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(rawBody, &result); err != nil {
		return "", fmt.Errorf("bkash: parse grant token response: %w", err)
	}
	if result.IDToken == "" {
		return "", fmt.Errorf("bkash: empty id_token in grant token response")
	}

	return result.IDToken, nil
}

// Initiate executes the bKash grant-token → create-payment flow and returns the
// bkashURL for redirect. ProviderIntentID is the bKash-assigned paymentID.
func (r *Rail) Initiate(ctx context.Context, input payments.InitiateInput) (payments.InitiateResult, error) {
	idToken, err := r.grantToken(ctx)
	if err != nil {
		return payments.InitiateResult{}, fmt.Errorf("bkash: initiate: %w", err)
	}

	// Format amount: AmountLocal is in paisa (1/100 BDT), present as decimal BDT string.
	// float64 is used here only for display formatting of an integer value, not for arithmetic.
	amountStr := fmt.Sprintf("%.2f", float64(input.AmountLocal)/100.0)

	createBody, err := json.Marshal(map[string]string{
		"mode":                  "0011",
		"payerReference":        input.AccountID.String(),
		"callbackURL":           input.CallbackBaseURL + "/webhooks/bkash/callback",
		"amount":                amountStr,
		"currency":              "BDT",
		"intent":                "sale",
		"merchantInvoiceNumber": input.PaymentIntentID.String(),
	})
	if err != nil {
		return payments.InitiateResult{}, fmt.Errorf("bkash: marshal create payment body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.baseURL+"/tokenized/checkout/create", bytes.NewReader(createBody))
	if err != nil {
		return payments.InitiateResult{}, fmt.Errorf("bkash: create payment request: %w", err)
	}
	req.Header.Set("Authorization", idToken)
	req.Header.Set("X-App-Key", r.appKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return payments.InitiateResult{}, fmt.Errorf("bkash: create payment request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return payments.InitiateResult{}, fmt.Errorf("bkash: create payment response status: %d", resp.StatusCode)
	}

	rawResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return payments.InitiateResult{}, fmt.Errorf("bkash: read create payment response: %w", err)
	}

	var createResult struct {
		PaymentID string `json:"paymentID"`
		BkashURL  string `json:"bkashURL"`
	}
	if err := json.Unmarshal(rawResp, &createResult); err != nil {
		return payments.InitiateResult{}, fmt.Errorf("bkash: parse create payment response: %w", err)
	}
	if createResult.PaymentID == "" {
		return payments.InitiateResult{}, fmt.Errorf("bkash: empty paymentID in create response")
	}
	if createResult.BkashURL == "" {
		return payments.InitiateResult{}, fmt.Errorf("bkash: empty bkashURL in create response")
	}

	return payments.InitiateResult{
		ProviderIntentID: createResult.PaymentID, // bKash-assigned paymentID, NOT merchantInvoiceNumber
		RedirectURL:      createResult.BkashURL,
		ExpiresAt:        time.Now().Add(10 * time.Minute),
	}, nil
}

// ProcessEvent parses the bKash callback body, then calls the Execute endpoint
// server-side to verify payment status. It never trusts callback query params alone.
//
// ProviderIntentID in the returned RailEvent is the bKash paymentID (provider-assigned),
// which matches the value stored by Initiate — not the merchantInvoiceNumber (Hive UUID).
func (r *Rail) ProcessEvent(ctx context.Context, rawBody []byte, _ map[string]string) (payments.RailEvent, error) {
	var callback struct {
		PaymentID string `json:"paymentID"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(rawBody, &callback); err != nil {
		return payments.RailEvent{}, fmt.Errorf("bkash: parse callback body: %w", err)
	}
	if callback.PaymentID == "" {
		return payments.RailEvent{}, fmt.Errorf("bkash: missing paymentID in callback")
	}

	// Always execute server-side to verify — never trust callback status alone.
	idToken, err := r.grantToken(ctx)
	if err != nil {
		return payments.RailEvent{}, fmt.Errorf("bkash: process event grant token: %w", err)
	}

	executeBody, err := json.Marshal(map[string]string{
		"paymentID": callback.PaymentID,
	})
	if err != nil {
		return payments.RailEvent{}, fmt.Errorf("bkash: marshal execute body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.baseURL+"/tokenized/checkout/execute", bytes.NewReader(executeBody))
	if err != nil {
		return payments.RailEvent{}, fmt.Errorf("bkash: create execute request: %w", err)
	}
	req.Header.Set("Authorization", idToken)
	req.Header.Set("X-App-Key", r.appKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return payments.RailEvent{}, fmt.Errorf("bkash: execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return payments.RailEvent{}, fmt.Errorf("bkash: execute response status: %d", resp.StatusCode)
	}

	rawExec, err := io.ReadAll(resp.Body)
	if err != nil {
		return payments.RailEvent{}, fmt.Errorf("bkash: read execute response: %w", err)
	}

	var executeResult struct {
		TransactionStatus string `json:"transactionStatus"`
	}
	if err := json.Unmarshal(rawExec, &executeResult); err != nil {
		return payments.RailEvent{}, fmt.Errorf("bkash: parse execute response: %w", err)
	}

	var eventType string
	if executeResult.TransactionStatus == "Completed" {
		eventType = "payment.succeeded"
	} else {
		eventType = "payment.failed"
	}

	return payments.RailEvent{
		ProviderIntentID: callback.PaymentID, // bKash paymentID, NOT merchantInvoiceNumber
		EventType:        eventType,
		RawPayload:       rawBody,
	}, nil
}
