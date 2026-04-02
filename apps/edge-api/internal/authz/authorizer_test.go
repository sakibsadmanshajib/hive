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
