package signupguard

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// ErrRateLimited is returned when an IP exceeds the per-window signup quota.
var ErrRateLimited = errors.New("signupguard: signup rate limit exceeded")

// ErrRateLimiterUnavailable is returned when the limiter cannot reach its
// backend (or has no client IP to scope on) and FailOpen is false. Per the
// #51 policy this fails CLOSED: abuse-control dependencies deny rather than
// admit unmetered traffic during a backend blip.
var ErrRateLimiterUnavailable = errors.New("signupguard: rate limiter unavailable")

// Incrementer increments a counter key and ensures it expires after ttl,
// returning the post-increment count. Implemented over Redis in production and
// faked in tests.
type Incrementer interface {
	IncrWithExpiry(ctx context.Context, key string, ttl time.Duration) (int64, error)
}

// RateLimitConfig configures a fixed-window limiter.
type RateLimitConfig struct {
	Limit    int           // max attempts per subject per window; <=0 disables the limiter
	Window   time.Duration // sliding fixed-window length
	FailOpen bool          // on backend error: false = deny (default), true = admit
	// Namespace and Subject label the Redis key so two limiters over the same
	// backend cannot share a counter. Both default to the original signup
	// values ("signup" and "ip"), so an existing caller that leaves them unset
	// produces byte-identical keys to before.
	Namespace string
	Subject   string
}

// RateLimiter enforces a fixed-window per-IP signup quota backed by an
// Incrementer. A nil backend or a non-positive Limit disables enforcement.
type RateLimiter struct {
	backend Incrementer
	cfg     RateLimitConfig
}

// NewRateLimiter constructs a per-IP limiter. Pass a nil backend to disable.
func NewRateLimiter(backend Incrementer, cfg RateLimitConfig) *RateLimiter {
	return &RateLimiter{backend: backend, cfg: cfg}
}

// Enabled reports whether the limiter will perform enforcement.
func (rl *RateLimiter) Enabled() bool {
	return rl != nil && rl.backend != nil && rl.cfg.Limit > 0
}

// Allow records one attempt for the given subject and reports whether it is
// within quota. The subject is a client IP for the signup limiter and a user id
// for the provisioning limiter; see RateLimitConfig.Subject. Returns
// ErrRateLimited when over quota, and ErrRateLimiterUnavailable when the
// backend errors (FailOpen=false) or the subject is missing (FailOpen=false).
func (rl *RateLimiter) Allow(ctx context.Context, subject string) error {
	if !rl.Enabled() {
		return nil
	}

	if subject == "" {
		// Nothing to scope on: fail closed unless explicitly told to fail open.
		if rl.cfg.FailOpen {
			return nil
		}
		return fmt.Errorf("%w: missing subject", ErrRateLimiterUnavailable)
	}

	window := rl.cfg.Window
	if window <= 0 {
		window = time.Hour
	}
	namespace := rl.cfg.Namespace
	if namespace == "" {
		namespace = "signup"
	}
	subjectLabel := rl.cfg.Subject
	if subjectLabel == "" {
		subjectLabel = "ip"
	}
	// Bucket the key by window so a fixed-window counter rolls over cleanly and
	// the TTL is a hard backstop against leaked keys. The braces are a Redis
	// Cluster hash tag, keeping a subject's buckets on one slot.
	bucket := time.Now().Unix() / int64(window/time.Second)
	key := fmt.Sprintf("%s:rl:{%s:%s}:%d", namespace, subjectLabel, subject, bucket)

	count, err := rl.backend.IncrWithExpiry(ctx, key, window)
	if err != nil {
		if rl.cfg.FailOpen {
			return nil
		}
		return fmt.Errorf("%w: %v", ErrRateLimiterUnavailable, err)
	}
	if count > int64(rl.cfg.Limit) {
		return ErrRateLimited
	}
	return nil
}

// RedisIncrementer adapts a go-redis client to the Incrementer interface using
// an INCR + EXPIRE pipeline. EXPIRE is only meaningful on the first increment,
// but issuing it every call is idempotent and cheap, and guarantees a TTL even
// if the very first EXPIRE was lost to a reconnect.
type RedisIncrementer struct {
	client *goredis.Client
}

// NewRedisIncrementer wraps a go-redis client. A nil client yields a nil
// Incrementer so the caller can disable rate limiting cleanly.
func NewRedisIncrementer(client *goredis.Client) Incrementer {
	if client == nil {
		return nil
	}
	return &RedisIncrementer{client: client}
}

// IncrWithExpiry runs INCR then EXPIRE in a pipeline and returns the count.
func (r *RedisIncrementer) IncrWithExpiry(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	pipe := r.client.TxPipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return incr.Val(), nil
}
