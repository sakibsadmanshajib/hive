package authz

import (
	"context"
	"strings"
	"testing"
	"time"
)

type slidingWindowCall struct {
	keys   []string
	limit  int
	amount int64
}

type longWindowCall struct {
	currentKey  string
	keyPrefix   string
	limit       int64
	score       int64
	bucketCount int
}

func TestLimiterUsesSeparateAccountAndKeyThresholds(t *testing.T) {
	var slidingCalls []slidingWindowCall

	limiter := &Limiter{
		now: func() time.Time {
			return time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC)
		},
		runSlidingWindow: func(_ context.Context, keys []string, limit int, amount int64, now time.Time) (bool, int, int, error) {
			slidingCalls = append(slidingCalls, slidingWindowCall{
				keys:   append([]string(nil), keys...),
				limit:  limit,
				amount: amount,
			})
			if strings.Contains(keys[0], "rl:{key:key-1:hive-default}:rpm:current") {
				return false, 0, 30, nil
			}
			return true, limit - 1, 30, nil
		},
		runLongWindow: func(_ context.Context, currentKey string, keyPrefix string, bucketSize time.Duration, bucketCount int, limit int64, score int64, now time.Time) (bool, int64, int, error) {
			return true, limit - score, 300, nil
		},
	}

	snapshot := AuthSnapshot{
		KeyID:     "key-1",
		AccountID: "acc-1",
		AccountRatePolicy: &RatePolicy{
			RateLimitRPM:          120,
			RateLimitTPM:          240000,
			RollingFiveHourLimit:  5000,
			WeeklyLimit:           10000,
			FreeTokenWeightTenths: 1,
		},
		KeyRatePolicy: &RatePolicy{
			RateLimitRPM:          12,
			RateLimitTPM:          24000,
			RollingFiveHourLimit:  500,
			WeeklyLimit:           1000,
			FreeTokenWeightTenths: 1,
		},
	}

	result, err := limiter.Check(context.Background(), snapshot, "hive-default", 15, 100, 20)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.Allowed {
		t.Fatal("expected key-scope denial")
	}
	if result.Reason != "request_limit_exceeded" {
		t.Fatalf("expected request_limit_exceeded, got %q", result.Reason)
	}
	if result.RequestLimit != 12 {
		t.Fatalf("expected key rpm limit 12, got %d", result.RequestLimit)
	}

	var sawAccountRPM bool
	var sawKeyRPM bool
	for _, call := range slidingCalls {
		if strings.Contains(call.keys[0], "rl:{acct:acc-1:hive-default}:rpm:current") && call.limit == 120 && call.amount == 1 {
			sawAccountRPM = true
		}
		if strings.Contains(call.keys[0], "rl:{key:key-1:hive-default}:rpm:current") && call.limit == 12 && call.amount == 1 {
			sawKeyRPM = true
		}
	}
	if !sawAccountRPM || !sawKeyRPM {
		t.Fatalf("expected separate account/key rpm checks, got %#v", slidingCalls)
	}
}

func TestWindowScoreUsesWeightedFreeTokens(t *testing.T) {
	var windowCalls []longWindowCall

	limiter := &Limiter{
		now: func() time.Time {
			return time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC)
		},
		runSlidingWindow: func(_ context.Context, keys []string, limit int, amount int64, now time.Time) (bool, int, int, error) {
			return true, limit - int(amount), 30, nil
		},
		runLongWindow: func(_ context.Context, currentKey string, keyPrefix string, bucketSize time.Duration, bucketCount int, limit int64, score int64, now time.Time) (bool, int64, int, error) {
			windowCalls = append(windowCalls, longWindowCall{
				currentKey:  currentKey,
				keyPrefix:   keyPrefix,
				limit:       limit,
				score:       score,
				bucketCount: bucketCount,
			})
			return true, limit - score, 300, nil
		},
	}

	snapshot := AuthSnapshot{
		KeyID:     "key-1",
		AccountID: "acc-1",
		AccountRatePolicy: &RatePolicy{
			RateLimitRPM:          120,
			RateLimitTPM:          240000,
			RollingFiveHourLimit:  5000,
			WeeklyLimit:           10000,
			FreeTokenWeightTenths: 3,
		},
		KeyRatePolicy: &RatePolicy{
			RateLimitRPM:          12,
			RateLimitTPM:          24000,
			RollingFiveHourLimit:  500,
			WeeklyLimit:           1000,
			FreeTokenWeightTenths: 5,
		},
	}

	_, err := limiter.Check(context.Background(), snapshot, "hive-default", 50, 100, 40)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	var accountScore int64 = -1
	var keyScore int64 = -1
	for _, call := range windowCalls {
		if strings.Contains(call.currentKey, "fraud:{acct:acc-1}:5h:") {
			accountScore = call.score
		}
		if strings.Contains(call.currentKey, "fraud:{key:key-1}:5h:") {
			keyScore = call.score
		}
	}

	if accountScore != 162 {
		t.Fatalf("expected account score 162, got %d from %#v", accountScore, windowCalls)
	}
	if keyScore != 170 {
		t.Fatalf("expected key score 170, got %d from %#v", keyScore, windowCalls)
	}
}

func TestLimiterRejectsMissingAccountPolicy(t *testing.T) {
	limiter := &Limiter{}

	_, err := limiter.Check(context.Background(), AuthSnapshot{
		KeyID:         "key-1",
		AccountID:     "acc-1",
		KeyRatePolicy: &RatePolicy{RateLimitRPM: 12, RateLimitTPM: 24000, FreeTokenWeightTenths: 1},
	}, "hive-default", 0, 0, 0)
	if err == nil {
		t.Fatal("expected missing account policy error")
	}
	if !strings.Contains(err.Error(), "account rate policy") {
		t.Fatalf("expected account policy error, got %v", err)
	}
}
