package signupguard

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeIncrementer struct {
	counts map[string]int64
	err    error
	calls  int
}

func (f *fakeIncrementer) IncrWithExpiry(_ context.Context, key string, _ time.Duration) (int64, error) {
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	if f.counts == nil {
		f.counts = map[string]int64{}
	}
	f.counts[key]++
	return f.counts[key], nil
}

func TestRateLimiterAllowsUnderLimit(t *testing.T) {
	inc := &fakeIncrementer{}
	rl := NewRateLimiter(inc, RateLimitConfig{Limit: 3, Window: time.Hour, FailOpen: false})
	for i := 1; i <= 3; i++ {
		if err := rl.Allow(context.Background(), "1.2.3.4"); err != nil {
			t.Fatalf("request %d under limit should pass, got %v", i, err)
		}
	}
}

func TestRateLimiterBlocksOverLimit(t *testing.T) {
	inc := &fakeIncrementer{}
	rl := NewRateLimiter(inc, RateLimitConfig{Limit: 2, Window: time.Hour})
	_ = rl.Allow(context.Background(), "1.2.3.4")
	_ = rl.Allow(context.Background(), "1.2.3.4")
	err := rl.Allow(context.Background(), "1.2.3.4")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("4th request should be rate limited, got %v", err)
	}
}

func TestRateLimiterScopesPerIP(t *testing.T) {
	inc := &fakeIncrementer{}
	rl := NewRateLimiter(inc, RateLimitConfig{Limit: 1, Window: time.Hour})
	if err := rl.Allow(context.Background(), "1.1.1.1"); err != nil {
		t.Fatalf("first IP first hit should pass: %v", err)
	}
	if err := rl.Allow(context.Background(), "2.2.2.2"); err != nil {
		t.Fatalf("different IP should have its own bucket: %v", err)
	}
	if err := rl.Allow(context.Background(), "1.1.1.1"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("first IP second hit should be limited, got %v", err)
	}
}

func TestRateLimiterFailClosedByDefault(t *testing.T) {
	inc := &fakeIncrementer{err: errors.New("redis down")}
	rl := NewRateLimiter(inc, RateLimitConfig{Limit: 5, Window: time.Hour, FailOpen: false})
	err := rl.Allow(context.Background(), "1.2.3.4")
	if !errors.Is(err, ErrRateLimiterUnavailable) {
		t.Fatalf("backend error must fail closed, got %v", err)
	}
}

func TestRateLimiterFailOpenWhenConfigured(t *testing.T) {
	inc := &fakeIncrementer{err: errors.New("redis down")}
	rl := NewRateLimiter(inc, RateLimitConfig{Limit: 5, Window: time.Hour, FailOpen: true})
	if err := rl.Allow(context.Background(), "1.2.3.4"); err != nil {
		t.Fatalf("fail-open mode should admit on backend error, got %v", err)
	}
}

func TestRateLimiterDisabledWhenNoBackend(t *testing.T) {
	// A nil incrementer means rate limiting is not configured; Allow must be a
	// no-op (the disposable + captcha controls still apply at the HTTP layer).
	rl := NewRateLimiter(nil, RateLimitConfig{Limit: 5, Window: time.Hour})
	if err := rl.Allow(context.Background(), "1.2.3.4"); err != nil {
		t.Fatalf("nil-backend limiter should be a no-op, got %v", err)
	}
}

func TestRateLimiterDisabledWhenLimitZero(t *testing.T) {
	inc := &fakeIncrementer{}
	rl := NewRateLimiter(inc, RateLimitConfig{Limit: 0, Window: time.Hour})
	if err := rl.Allow(context.Background(), "1.2.3.4"); err != nil {
		t.Fatalf("zero limit disables rate limiting, got %v", err)
	}
	if inc.calls != 0 {
		t.Fatalf("zero limit must not touch the backend, calls=%d", inc.calls)
	}
}

func TestRateLimiterEmptyIPFailsClosed(t *testing.T) {
	inc := &fakeIncrementer{}
	rl := NewRateLimiter(inc, RateLimitConfig{Limit: 5, Window: time.Hour, FailOpen: false})
	// Missing client IP must not silently bypass the limiter.
	if err := rl.Allow(context.Background(), ""); !errors.Is(err, ErrRateLimiterUnavailable) {
		t.Fatalf("empty IP should fail closed, got %v", err)
	}
	if inc.calls != 0 {
		t.Fatalf("empty IP must not hit the backend, calls=%d", inc.calls)
	}
}

func TestRateLimiterEmptyIPFailsOpenWhenConfigured(t *testing.T) {
	inc := &fakeIncrementer{}
	rl := NewRateLimiter(inc, RateLimitConfig{Limit: 5, Window: time.Hour, FailOpen: true})
	if err := rl.Allow(context.Background(), ""); err != nil {
		t.Fatalf("empty IP under fail-open should admit, got %v", err)
	}
}
