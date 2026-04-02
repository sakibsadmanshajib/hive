package authz

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	accountRPMCurrentPattern  = "rl:{acct:<account_id>:<alias_id>}:rpm:current"
	accountRPMPreviousPattern = "rl:{acct:<account_id>:<alias_id>}:rpm:previous"
	accountTPMCurrentPattern  = "rl:{acct:<account_id>:<alias_id>}:tpm:current"
	accountTPMPreviousPattern = "rl:{acct:<account_id>:<alias_id>}:tpm:previous"
	keyRPMCurrentPattern      = "rl:{key:<key_id>:<alias_id>}:rpm:current"
	keyRPMPreviousPattern     = "rl:{key:<key_id>:<alias_id>}:rpm:previous"
	keyTPMCurrentPattern      = "rl:{key:<key_id>:<alias_id>}:tpm:current"
	keyTPMPreviousPattern     = "rl:{key:<key_id>:<alias_id>}:tpm:previous"
	accountFiveHourPattern    = "fraud:{acct:<account_id>}:5h:<bucket>"
	keyFiveHourPattern        = "fraud:{key:<key_id>}:5h:<bucket>"
	accountWeeklyPattern      = "fraud:{acct:<account_id>}:7d:<bucket>"
	keyWeeklyPattern          = "fraud:{key:<key_id>}:7d:<bucket>"
)

//go:embed scripts/rpm_tpm.lua
var rpmTPMLua string

//go:embed scripts/window_score.lua
var windowScoreLua string

// LimitResult describes the outcome of an edge hot-path limiter check.
type LimitResult struct {
	Allowed             bool
	Reason              string
	RequestLimit        int
	RequestRemaining    int
	RequestResetSeconds int
	TokenLimit          int
	TokenRemaining      int
	TokenResetSeconds   int
}

// Limiter enforces projected account/key thresholds via Redis-backed scripts.
type Limiter struct {
	redis             *redis.Client
	rpmTPMScript      *redis.Script
	windowScoreScript *redis.Script

	// CheckOverride is a test hook for bypassing Redis-backed limiter logic.
	CheckOverride func(ctx context.Context, snapshot AuthSnapshot, aliasID string, estimatedCredits, billableTokens, freeTokens int64) (LimitResult, error)

	now              func() time.Time
	runSlidingWindow func(ctx context.Context, keys []string, limit int, amount int64, now time.Time) (bool, int, int, error)
	runLongWindow    func(ctx context.Context, currentKey string, keyPrefix string, bucketSize time.Duration, bucketCount int, limit int64, score int64, now time.Time) (bool, int64, int, error)
}

// NewLimiter creates a Redis-backed edge limiter.
func NewLimiter(redisURL string) (*Limiter, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("authz: parse redis URL: %w", err)
	}

	limiter := &Limiter{
		redis:             redis.NewClient(opt),
		rpmTPMScript:      redis.NewScript(rpmTPMLua),
		windowScoreScript: redis.NewScript(windowScoreLua),
		now:               time.Now,
	}
	limiter.runSlidingWindow = limiter.defaultRunSlidingWindow
	limiter.runLongWindow = limiter.defaultRunLongWindow
	return limiter, nil
}

// Check enforces account and key rate limits independently.
func (l *Limiter) Check(ctx context.Context, snapshot AuthSnapshot, aliasID string, estimatedCredits int64, billableTokens int64, freeTokens int64) (LimitResult, error) {
	if l == nil {
		return LimitResult{Allowed: true}, nil
	}
	if l.CheckOverride != nil {
		return l.CheckOverride(ctx, snapshot, aliasID, estimatedCredits, billableTokens, freeTokens)
	}
	if snapshot.AccountRatePolicy == nil {
		return LimitResult{}, errors.New("authz: missing account rate policy")
	}
	if snapshot.KeyRatePolicy == nil {
		return LimitResult{}, errors.New("authz: missing key rate policy")
	}

	l.ensureDefaults()

	now := l.now()
	if aliasID == "" {
		aliasID = "__all__"
	}

	accountScope := fmt.Sprintf("acct:%s:%s", snapshot.AccountID, aliasID)
	keyScope := fmt.Sprintf("key:%s:%s", snapshot.KeyID, aliasID)
	accountFraudPrefix := fmt.Sprintf("fraud:{acct:%s}", snapshot.AccountID)
	keyFraudPrefix := fmt.Sprintf("fraud:{key:%s}", snapshot.KeyID)

	if result, err := l.checkScope(ctx, accountScope, accountFraudPrefix, snapshot.AccountRatePolicy, estimatedCredits, billableTokens, freeTokens, now); err != nil || !result.Allowed {
		return result, err
	}
	if result, err := l.checkScope(ctx, keyScope, keyFraudPrefix, snapshot.KeyRatePolicy, estimatedCredits, billableTokens, freeTokens, now); err != nil || !result.Allowed {
		return result, err
	}

	return LimitResult{Allowed: true}, nil
}

func (l *Limiter) ensureDefaults() {
	if l.now == nil {
		l.now = time.Now
	}
	if l.runSlidingWindow == nil {
		l.runSlidingWindow = l.defaultRunSlidingWindow
	}
	if l.runLongWindow == nil {
		l.runLongWindow = l.defaultRunLongWindow
	}
}

func (l *Limiter) checkScope(ctx context.Context, slidingScope string, fraudPrefix string, policy *RatePolicy, estimatedCredits int64, billableTokens int64, freeTokens int64, now time.Time) (LimitResult, error) {
	totalTokens := billableTokens + freeTokens

	if policy.RateLimitRPM > 0 {
		allowed, remaining, reset, err := l.runSlidingWindow(ctx, slidingWindowKeys(slidingScope, "rpm"), policy.RateLimitRPM, 1, now)
		if err != nil {
			return LimitResult{}, err
		}
		if !allowed {
			return LimitResult{
				Allowed:             false,
				Reason:              "request_limit_exceeded",
				RequestLimit:        policy.RateLimitRPM,
				RequestRemaining:    maxInt(remaining, 0),
				RequestResetSeconds: maxInt(reset, 0),
			}, nil
		}
	}

	if policy.RateLimitTPM > 0 {
		allowed, remaining, reset, err := l.runSlidingWindow(ctx, slidingWindowKeys(slidingScope, "tpm"), policy.RateLimitTPM, totalTokens, now)
		if err != nil {
			return LimitResult{}, err
		}
		if !allowed {
			return LimitResult{
				Allowed:           false,
				Reason:            "token_limit_exceeded",
				TokenLimit:        policy.RateLimitTPM,
				TokenRemaining:    maxInt(remaining, 0),
				TokenResetSeconds: maxInt(reset, 0),
			}, nil
		}
	}

	score := weightedWindowScore(*policy, estimatedCredits, billableTokens, freeTokens)

	if policy.RollingFiveHourLimit > 0 {
		currentKey, keyPrefix := fraudWindowLocation(fraudPrefix, "5h", 5*time.Minute, now)
		allowed, _, reset, err := l.runLongWindow(ctx, currentKey, keyPrefix, 5*time.Minute, 60, policy.RollingFiveHourLimit, score, now)
		if err != nil {
			return LimitResult{}, err
		}
		if !allowed {
			return LimitResult{
				Allowed:             false,
				Reason:              "quota_window_exceeded",
				RequestResetSeconds: maxInt(reset, 0),
			}, nil
		}
	}

	if policy.WeeklyLimit > 0 {
		currentKey, keyPrefix := fraudWindowLocation(fraudPrefix, "7d", 24*time.Hour, now)
		allowed, _, reset, err := l.runLongWindow(ctx, currentKey, keyPrefix, 24*time.Hour, 7, policy.WeeklyLimit, score, now)
		if err != nil {
			return LimitResult{}, err
		}
		if !allowed {
			return LimitResult{
				Allowed:             false,
				Reason:              "quota_window_exceeded",
				RequestResetSeconds: maxInt(reset, 0),
			}, nil
		}
	}

	return LimitResult{Allowed: true}, nil
}

func (l *Limiter) defaultRunSlidingWindow(ctx context.Context, keys []string, limit int, amount int64, now time.Time) (bool, int, int, error) {
	if l.redis == nil || l.rpmTPMScript == nil {
		return false, 0, 0, errors.New("authz: limiter sliding-window script unavailable")
	}

	currentStart := now.Truncate(time.Minute)
	previousStart := currentStart.Add(-time.Minute)
	result, err := l.rpmTPMScript.Run(
		ctx,
		l.redis,
		keys,
		currentStart.UnixMilli(),
		previousStart.UnixMilli(),
		now.UnixMilli(),
		limit,
		amount,
	).Result()
	if err != nil {
		return false, 0, 0, fmt.Errorf("authz: run rpm/tpm limiter: %w", err)
	}

	allowed, remaining, resetMs, err := parseRedisLimiterResult(result)
	if err != nil {
		return false, 0, 0, err
	}
	return allowed, remaining, msToSeconds(resetMs), nil
}

func (l *Limiter) defaultRunLongWindow(ctx context.Context, currentKey string, keyPrefix string, bucketSize time.Duration, bucketCount int, limit int64, score int64, now time.Time) (bool, int64, int, error) {
	if l.redis == nil || l.windowScoreScript == nil {
		return false, 0, 0, errors.New("authz: limiter long-window script unavailable")
	}

	currentBucket := bucketID(now, bucketSize)
	result, err := l.windowScoreScript.Run(
		ctx,
		l.redis,
		[]string{currentKey},
		keyPrefix,
		currentBucket,
		bucketSize.Milliseconds(),
		bucketCount,
		limit,
		score,
		now.UnixMilli(),
	).Result()
	if err != nil {
		return false, 0, 0, fmt.Errorf("authz: run long-window limiter: %w", err)
	}

	allowed, remaining, resetMs, err := parseRedisLimiterResult(result)
	if err != nil {
		return false, 0, 0, err
	}
	return allowed, int64(remaining), msToSeconds(resetMs), nil
}

func slidingWindowKeys(scope string, metric string) []string {
	return []string{
		fmt.Sprintf("rl:{%s}:%s:current", scope, metric),
		fmt.Sprintf("rl:{%s}:%s:previous", scope, metric),
	}
}

func fraudWindowLocation(prefix string, family string, bucketSize time.Duration, now time.Time) (string, string) {
	keyPrefix := fmt.Sprintf("%s:%s", prefix, family)
	return fmt.Sprintf("%s:%d", keyPrefix, bucketID(now, bucketSize)), keyPrefix
}

func bucketID(now time.Time, bucketSize time.Duration) int64 {
	return now.Unix() / int64(bucketSize/time.Second)
}

func weightedWindowScore(policy RatePolicy, estimatedCredits int64, billableTokens int64, freeTokens int64) int64 {
	weight := policy.FreeTokenWeightTenths
	if weight < 1 {
		weight = 1
	}
	return estimatedCredits + billableTokens + (freeTokens*int64(weight))/10
}

func parseRedisLimiterResult(result any) (bool, int, int, error) {
	values, ok := result.([]any)
	if !ok || len(values) != 3 {
		return false, 0, 0, fmt.Errorf("authz: unexpected limiter result %#v", result)
	}

	allowed, err := asInt64(values[0])
	if err != nil {
		return false, 0, 0, err
	}
	remaining, err := asInt64(values[1])
	if err != nil {
		return false, 0, 0, err
	}
	resetMs, err := asInt64(values[2])
	if err != nil {
		return false, 0, 0, err
	}

	return allowed == 1, int(remaining), int(resetMs), nil
}

func asInt64(value any) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case string:
		return strconv.ParseInt(v, 10, 64)
	case []byte:
		return strconv.ParseInt(string(v), 10, 64)
	default:
		return 0, fmt.Errorf("authz: unexpected numeric type %T", value)
	}
}

func msToSeconds(ms int) int {
	if ms <= 0 {
		return 0
	}
	return (ms + 999) / 1000
}

func maxInt(v int, floor int) int {
	if v < floor {
		return floor
	}
	return v
}
