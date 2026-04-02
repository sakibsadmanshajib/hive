package authz

import (
	"context"
	"testing"
)

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
