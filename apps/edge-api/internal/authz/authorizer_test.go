package authz

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// #51: a Redis-backed limiter that cannot evaluate (transient outage,
// connection/auth failure) must NOT silently bypass rate limiting in
// production. Default policy is fail-closed: the request is denied with a
// retryable 429 rather than admitted unmetered.
func TestAuthorizeFailsClosedWhenLimiterErrors(t *testing.T) {
	client := &Client{
		ResolveOverride: func(_ context.Context, _ string) (AuthSnapshot, error) {
			return AuthSnapshot{
				KeyID:             "key-1",
				AccountID:         "acc-1",
				Status:            "active",
				AllowAllModels:    true,
				BudgetKind:        "none",
				AccountRatePolicy: &RatePolicy{RateLimitRPM: 120},
				KeyRatePolicy:     &RatePolicy{RateLimitRPM: 12},
			}, nil
		},
	}
	limiter := &Limiter{
		CheckOverride: func(_ context.Context, _ AuthSnapshot, _ string, _, _, _ int64) (LimitResult, error) {
			return LimitResult{}, errors.New("authz: run rpm/tpm limiter: redis: connection refused")
		},
	}

	authorizer := NewAuthorizer(client, limiter)

	_, headers, err := authorizer.Authorize(context.Background(), "Bearer hk_test", "hive-default", 50, 100, 20)
	if err == nil {
		t.Fatal("expected fail-closed rate-limit error when limiter backend errors")
	}
	if err.Error.Type != "rate_limit_error" {
		t.Fatalf("expected rate_limit_error type, got %q", err.Error.Type)
	}
	if err.Error.Code == nil || *err.Error.Code != "rate_limit_exceeded" {
		t.Fatalf("expected rate_limit_exceeded code, got %#v", err.Error.Code)
	}
	if headers["retry-after"] == "" {
		t.Fatalf("expected retry-after header on degraded-limiter denial, got %#v", headers)
	}
	// Provider-blind: the customer-facing message must not leak the backend
	// (redis) or any internal error text.
	if msg := err.Error.Message; containsAny(msg, "redis", "connection refused", "rpm/tpm") {
		t.Fatalf("error message leaks backend internals: %q", msg)
	}
}

// #51: an operator may explicitly opt into fail-open (dev/local) so a Redis
// outage degrades to unmetered rather than denying. This must be a deliberate
// toggle, never the default.
func TestAuthorizeFailsOpenWhenExplicitlyEnabled(t *testing.T) {
	client := &Client{
		ResolveOverride: func(_ context.Context, _ string) (AuthSnapshot, error) {
			return AuthSnapshot{
				KeyID:             "key-1",
				AccountID:         "acc-1",
				Status:            "active",
				AllowAllModels:    true,
				BudgetKind:        "none",
				AccountRatePolicy: &RatePolicy{RateLimitRPM: 120},
				KeyRatePolicy:     &RatePolicy{RateLimitRPM: 12},
			}, nil
		},
	}
	limiter := &Limiter{
		CheckOverride: func(_ context.Context, _ AuthSnapshot, _ string, _, _, _ int64) (LimitResult, error) {
			return LimitResult{}, errors.New("redis down")
		},
	}

	authorizer := NewAuthorizer(client, limiter, WithFailOpen(true))

	snapshot, _, err := authorizer.Authorize(context.Background(), "Bearer hk_test", "hive-default", 50, 100, 20)
	if err != nil {
		t.Fatalf("expected fail-open admit when explicitly enabled, got %#v", err)
	}
	if snapshot.KeyID != "key-1" {
		t.Fatalf("expected resolved snapshot returned on fail-open, got %#v", snapshot)
	}
}

// #566/#569 postmortem: a key-resolution failure (not found, revoked,
// control-plane/network error) must reach the caller as the same generic
// invalid_api_key message regardless of cause -- never let a caller
// enumerate which keys exist -- but the real cause must still be visible
// server-side, or an outage like this one is undiagnosable from logs alone.
func TestAuthorizeLogsResolutionFailureCauseButKeepsClientMessageGeneric(t *testing.T) {
	client := &Client{
		ResolveOverride: func(_ context.Context, _ string) (AuthSnapshot, error) {
			return AuthSnapshot{}, errors.New(`authz: status 404: {"error":"key not found"}`)
		},
	}

	authorizer := NewAuthorizer(client, nil)

	var logOutput string
	var err *apierrors.OpenAIError
	logOutput = captureAuthzLogs(t, func() {
		_, _, err = authorizer.Authorize(context.Background(), "Bearer hk_test", "hive-default", 50, 100, 20)
	})

	// Client-facing half: byte-identical generic message, no hint of the
	// underlying resolve-path outcome.
	if err == nil {
		t.Fatal("expected invalid_api_key error")
	}
	if err.Error.Type != "invalid_request_error" {
		t.Fatalf("type = %q, want %q", err.Error.Type, "invalid_request_error")
	}
	if err.Error.Code == nil || *err.Error.Code != "invalid_api_key" {
		t.Fatalf("code = %v, want %q", err.Error.Code, "invalid_api_key")
	}
	if err.Error.Message != "Incorrect API key provided." {
		t.Fatalf("message = %q, want byte-identical generic message", err.Error.Message)
	}

	// Internal half: the distinguishable cause must be visible in logs.
	if !strings.Contains(logOutput, "key not found") {
		t.Fatalf("expected resolution failure cause in internal log, got %q", logOutput)
	}
}

// Companion case for the CheckAccess deny path (key found but revoked):
// check.DenyMsg already differentiates this from a not-found/network failure
// in the client message (pre-existing behavior, unchanged by this test), and
// that same cause must now also land in the internal log.
func TestAuthorizeLogsDeniedReasonForRevokedKey(t *testing.T) {
	client := &Client{
		ResolveOverride: func(_ context.Context, _ string) (AuthSnapshot, error) {
			return AuthSnapshot{
				KeyID:     "key-1",
				AccountID: "acc-1",
				Status:    "revoked",
			}, nil
		},
	}

	authorizer := NewAuthorizer(client, nil)

	var err *apierrors.OpenAIError
	logOutput := captureAuthzLogs(t, func() {
		_, _, err = authorizer.Authorize(context.Background(), "Bearer hk_test", "hive-default", 50, 100, 20)
	})

	if err == nil {
		t.Fatal("expected invalid_api_key error")
	}
	if err.Error.Code == nil || *err.Error.Code != "invalid_api_key" {
		t.Fatalf("code = %v, want %q", err.Error.Code, "invalid_api_key")
	}
	if err.Error.Message != "Incorrect API key provided: API key is revoked" {
		t.Fatalf("message = %q, want unchanged revoked message", err.Error.Message)
	}

	if !strings.Contains(logOutput, "revoked") {
		t.Fatalf("expected revoked reason in internal log, got %q", logOutput)
	}
}

func captureAuthzLogs(t *testing.T, fn func()) string {
	t.Helper()

	var buf bytes.Buffer
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer log.SetOutput(originalWriter)
	defer log.SetFlags(originalFlags)

	fn()
	return buf.String()
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(strings.ToLower(s), sub) {
			return true
		}
	}
	return false
}

func TestAuthorizeReturnsRateLimitHeadersOnlyFor429(t *testing.T) {
	client := &Client{
		ResolveOverride: func(_ context.Context, rawToken string) (AuthSnapshot, error) {
			return AuthSnapshot{
				KeyID:             "key-1",
				AccountID:         "acc-1",
				Status:            "active",
				AllowAllModels:    true,
				BudgetKind:        "none",
				AccountRatePolicy: &RatePolicy{RateLimitRPM: 120, RateLimitTPM: 240000, FreeTokenWeightTenths: 1},
				KeyRatePolicy:     &RatePolicy{RateLimitRPM: 12, RateLimitTPM: 24000, FreeTokenWeightTenths: 1},
			}, nil
		},
	}
	limiter := &Limiter{
		CheckOverride: func(_ context.Context, snapshot AuthSnapshot, aliasID string, estimatedCredits, billableTokens, freeTokens int64) (LimitResult, error) {
			return LimitResult{
				Allowed:             false,
				Reason:              "request_limit_exceeded",
				RequestLimit:        12,
				RequestRemaining:    0,
				RequestResetSeconds: 17,
			}, nil
		},
	}

	authorizer := NewAuthorizer(client, limiter)

	_, headers, err := authorizer.Authorize(context.Background(), "Bearer hk_test", "hive-default", 50, 100, 20)
	if err == nil {
		t.Fatal("expected rate-limit error")
	}
	if err.Error.Code == nil || *err.Error.Code != "rate_limit_exceeded" {
		t.Fatalf("expected rate_limit_exceeded code, got %#v", err.Error.Code)
	}
	if headers["x-ratelimit-limit-requests"] != "12" {
		t.Fatalf("expected request limit header, got %#v", headers)
	}
	if headers["retry-after"] != "17" {
		t.Fatalf("expected retry-after header, got %#v", headers)
	}
	if headers["x-ratelimit-limit-tokens"] != "" {
		t.Fatalf("expected token headers omitted on request-only limit, got %#v", headers)
	}
}

func TestAuthorizeReturnsInsufficientQuotaOnProjectedBudgetOverrun(t *testing.T) {
	limit := int64(1000)
	client := &Client{
		ResolveOverride: func(_ context.Context, rawToken string) (AuthSnapshot, error) {
			return AuthSnapshot{
				KeyID:                 "key-1",
				AccountID:             "acc-1",
				Status:                "active",
				AllowAllModels:        true,
				BudgetKind:            "monthly",
				BudgetLimitCredits:    &limit,
				BudgetConsumedCredits: 850,
				BudgetReservedCredits: 100,
			}, nil
		},
	}

	authorizer := NewAuthorizer(client, nil)

	_, headers, err := authorizer.Authorize(context.Background(), "Bearer hk_test", "hive-default", 100, 0, 0)
	if err == nil {
		t.Fatal("expected insufficient_quota error")
	}
	if len(headers) != 0 {
		t.Fatalf("expected no retry headers for quota errors, got %#v", headers)
	}
	if err.Error.Type != "insufficient_quota" {
		t.Fatalf("expected insufficient_quota type, got %q", err.Error.Type)
	}
	if err.Error.Code == nil || *err.Error.Code != "insufficient_quota" {
		t.Fatalf("expected insufficient_quota code, got %#v", err.Error.Code)
	}
}
