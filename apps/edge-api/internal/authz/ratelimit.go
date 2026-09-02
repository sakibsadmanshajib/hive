package authz

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/sakibsadmanshajib/hive/packages/ratewindows"
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
	// Long-window bucket keys are built by packages/ratewindows, which
	// control-plane reads back for the consumption display. The shapes are
	// "rlwin:{acct:<account_id>}:session:<bucket>" and its weekly twin.
	//
	// They used to be spelled "fraud:*" here and nowhere else. The prefix was
	// wrong twice over: these windows carry the customer's own subscription
	// allowance (D-069), and a refusal now names the window to the customer,
	// so a key namespace that calls them fraud counters is a working system
	// misdescribing itself to the next reader. Nothing was migrated because
	// there was nothing to migrate: every one of these limits was zero, so
	// Redis held no such key on any deployment (issue #1725).
)

//go:embed scripts/rpm_tpm.lua
var rpmTPMLua string

//go:embed scripts/window_score.lua
var windowScoreLua string

// WindowState reports one long usage window's configuration and consumption,
// on an allowed check as well as a refused one. It is what makes a limit
// visible: headers on a 200, and the console and chat percentage bars.
//
// Configured is carried separately from Limit rather than inferred from
// "Limit > 0" at each call site, because those two states have to read
// differently to a customer. An unset window is unlimited, and a surface that
// prints an empty bar for it claims a limit the account does not have.
type WindowState struct {
	Name       string
	Configured bool
	Limit      int64
	Used       int64
	Remaining  int64
	// ResetAt is when the window is empty again if nothing further is spent.
	// Zero only when the window is unconfigured.
	ResetAt time.Time
	// ResetSeconds is ResetAt as a duration from the instant of the check.
	// Carried rather than recomputed against time.Now() at render time, so a
	// header and the body it ships beside cannot disagree, and so a test can
	// assert an exact value against a fixed clock.
	ResetSeconds int
}

// UsedPercent and RemainingPercent are how a window reaches a customer, and
// the only way it may.
//
// Limit and Used are a weighted score denominated in Hive credits, and credits
// convert to dollars by a constant this product publishes in its own console
// (CREDITS_PER_USD). Emitting the raw allowance would therefore hand every
// subscriber the internal credit value of their plan, which D-068 makes
// confidential, and would put a currency figure on a customer surface, which
// D-070 forbids. A percentage carries everything the customer needs to pace
// themselves and none of that.
func (w WindowState) UsedPercent() int {
	if !w.Configured || w.Limit <= 0 {
		return 0
	}
	pct := w.Used * 100 / w.Limit
	if pct > 100 {
		return 100
	}
	if pct < 0 {
		return 0
	}
	return int(pct)
}

// RemainingPercent is the complement of UsedPercent.
func (w WindowState) RemainingPercent() int {
	return 100 - w.UsedPercent()
}

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

	// RetryAfterSeconds is how long the caller should wait before retrying,
	// whichever limit refused. Separate from RequestResetSeconds on purpose:
	// that one names the requests-per-minute window, and a long-window refusal
	// writing a four hour reset into it produced an x-ratelimit-reset-requests
	// header that reads as a per-minute limiter with no idea what a minute is.
	RetryAfterSeconds int

	// Window names the long window that refused, empty when none did.
	Window string
	// ResetAt is the real reset instant for the refusal: the moment enough
	// of the window ages out for this request to fit. Previously the limiter
	// reported only "seconds until the current bucket rolls", which is a
	// number about Redis bookkeeping rather than about the customer's
	// allowance, and the refusal text carried no time at all.
	ResetAt time.Time

	Session WindowState
	Weekly  WindowState
}

// longWindowResult is one long-window script run.
type longWindowResult struct {
	Allowed   bool
	Remaining int64
	Used      int64
	// RetryAfter is when enough score ages out for THIS request to fit.
	RetryAfter time.Time
	// ResetAt is when the window drains completely.
	ResetAt time.Time
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
	runLongWindow    func(ctx context.Context, familyPrefix string, shape ratewindows.Shape, anchor time.Time, limit int64, score int64, now time.Time) (longWindowResult, error)
	// commitLongWindows charges the windows that admitted. Separate from
	// runLongWindow, which now only scores, so that a request refused by any
	// window is charged against none of them.
	commitLongWindows func(ctx context.Context, charges []pendingCharge) error
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
	limiter.commitLongWindows = limiter.defaultCommitLongWindows
	return limiter, nil
}

// CheckWithTier runs Check and additionally enforces a tier-scoped sliding-window
// bucket whose effective per-dimension limit is min(keyLimit, tierLimit).
// Phase 12 wires this seam from the proxy hot-path: account+key checks first,
// then a tier check at scope tier:<tier>:<account_id> using the merged
// (key vs tier) effective limit. The tier check writes a separate Redis key —
// so a tight key limit and a loose tier limit do NOT consume the tier bucket
// twice when account+key already deny. Tier-binding info populates LimitType.
func (l *Limiter) CheckWithTier(
	ctx context.Context,
	snapshot AuthSnapshot,
	aliasID string,
	tier Tier,
	tierLimits TierLimits,
	estimatedCredits int64,
	billableTokens int64,
	freeTokens int64,
) (LimitResult, error) {
	// Run the existing account+key path first. If it denies, return immediately.
	base, err := l.Check(ctx, snapshot, aliasID, estimatedCredits, billableTokens, freeTokens)
	if err != nil || !base.Allowed {
		return base, err
	}
	if l == nil || tier == "" {
		return base, nil
	}

	// Layer the tier-scoped bucket. Effective per-dimension limit is
	// min(keyLimit, tierLimit) where 0 means "no limit at that layer".
	keyRPM := 0
	keyTPM := 0
	if snapshot.KeyRatePolicy != nil {
		keyRPM = snapshot.KeyRatePolicy.RateLimitRPM
		keyTPM = snapshot.KeyRatePolicy.RateLimitTPM
		// Per-key tier_overrides take precedence over env defaults.
		if override, ok := snapshot.KeyRatePolicy.TierOverrides[string(tier)]; ok {
			if override.RPM > 0 {
				tierLimits.RPM = override.RPM
			}
			if override.TPM > 0 {
				tierLimits.TPM = override.TPM
			}
		}
	}
	effectiveRPM := MinPositive(keyRPM, tierLimits.RPM)
	effectiveTPM := MinPositive(keyTPM, tierLimits.TPM)

	if effectiveRPM <= 0 && effectiveTPM <= 0 {
		return base, nil
	}

	l.ensureDefaults()
	now := l.now()
	tierScope := fmt.Sprintf("tier:%s:%s", tier, snapshot.AccountID)

	if effectiveRPM > 0 {
		allowed, remaining, reset, err := l.runSlidingWindow(ctx, slidingWindowKeys(tierScope, "rpm"), effectiveRPM, 1, now)
		if err != nil {
			return LimitResult{}, err
		}
		if !allowed {
			return LimitResult{
				Allowed:             false,
				Reason:              "request_limit_exceeded",
				RequestLimit:        effectiveRPM,
				RequestRemaining:    maxInt(remaining, 0),
				RequestResetSeconds: maxInt(reset, 0),
				RetryAfterSeconds:   maxInt(reset, 0),
			}, nil
		}
	}
	if effectiveTPM > 0 {
		totalTokens := billableTokens + freeTokens
		allowed, remaining, reset, err := l.runSlidingWindow(ctx, slidingWindowKeys(tierScope, "tpm"), effectiveTPM, totalTokens, now)
		if err != nil {
			return LimitResult{}, err
		}
		if !allowed {
			return LimitResult{
				Allowed:           false,
				Reason:            "token_limit_exceeded",
				TokenLimit:        effectiveTPM,
				TokenRemaining:    maxInt(remaining, 0),
				TokenResetSeconds: maxInt(reset, 0),
				RetryAfterSeconds: maxInt(reset, 0),
			}, nil
		}
	}

	// The account and key windows have already been charged by Check, so a
	// refusal here still spends them. Nothing outside tests calls this method
	// (see tier.go), and wiring it would mean threading the charge pass out of
	// Check; the honest note is cheaper than the machinery until it is wired.
	return base, nil
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
	accountWindows := ratewindows.AccountPrefix(snapshot.AccountID)
	keyWindows := ratewindows.KeyPrefix(snapshot.KeyID)

	// The weekly anchor is the ACCOUNT's, for both scopes. A per-key anchor
	// would let one account's week end at several different instants, which is
	// unexplainable on a customer surface and unenforceable as one allowance.
	//
	// One rule, in one place, shared with the control-plane reader that draws
	// the consumption bar (ratewindows.ParseWeeklyAnchor). There is no epoch
	// fallback any more: an anchor the two sides resolve differently means two
	// different Redis keys, and the counts written to the one nobody reads back
	// are gone for good.
	anchor, anchored := ratewindows.ParseWeeklyAnchor(snapshot.AccountRatePolicy.WeeklyAnchorAt, now)
	if !anchored && (snapshot.AccountRatePolicy.WeeklyLimit > 0 || snapshot.KeyRatePolicy.WeeklyLimit > 0) {
		// The deploy-skew case: a new edge-api reading an auth snapshot an
		// older control-plane cached, which carries no anchor field at all.
		// Under-enforcing the weekly window for the snapshot TTL is the same
		// trade this subsystem already makes for a stale limit VALUE, and it
		// is strictly better than counting into a grid the console never
		// reads. Loud, because the alternative is a silent absence.
		slog.Warn("weekly usage window skipped: auth snapshot carries no usable weekly anchor",
			"account_id", snapshot.AccountID,
			"key_id", snapshot.KeyID)
	}

	accountResult, accountPending, err := l.scoreScope(ctx, accountScope, accountWindows, snapshot.AccountRatePolicy, anchor, anchored, estimatedCredits, billableTokens, freeTokens, now)
	if err != nil || !accountResult.Allowed {
		return accountResult, err
	}
	keyResult, keyPending, err := l.scoreScope(ctx, keyScope, keyWindows, snapshot.KeyRatePolicy, anchor, anchored, estimatedCredits, billableTokens, freeTokens, now)
	if err != nil {
		return LimitResult{}, err
	}
	if !keyResult.Allowed {
		// Nothing has been charged: both scopes were SCORED, and the charge
		// below only happens when every window admitted. Fill in the windows
		// the key scope left unconfigured so a header set is never blank, but
		// never overwrite the refusing scope's own state with the other
		// scope's -- a 429 that names the session window has to carry the
		// session numbers of the scope that actually refused.
		keyResult.Session = fillWindow(keyResult.Session, accountResult.Session)
		keyResult.Weekly = fillWindow(keyResult.Weekly, accountResult.Weekly)
		return keyResult, nil
	}

	// Every window in both scopes admitted, so the request may be charged.
	//
	// Scoring and charging are two passes on purpose. When they were one, the
	// account's session window INCRBYed before the account's weekly window was
	// consulted at all, and the account's windows before the key's, so a
	// request refused by any later window had already spent the allowance of
	// every earlier one -- and a client obeying the retry-after it was handed
	// burned the rest down on every attempt. The component that refused the
	// request was the component charging for it.
	//
	// The cost of the split is that the score and the charge are no longer one
	// atomic script, so N requests scoring concurrently against the same
	// window can each admit and overshoot the allowance by up to N-1 requests'
	// worth of score. That is bounded, self-correcting on the next request,
	// and on the generous side of the customer; charging for refusals is
	// neither.
	pending := append(append([]pendingCharge(nil), accountPending...), keyPending...)
	if len(pending) > 0 {
		if err := l.commitLongWindows(ctx, pending); err != nil {
			return LimitResult{}, err
		}
		applyCharges(&accountResult, accountPending)
		applyCharges(&keyResult, keyPending)
	}

	merged := LimitResult{
		Allowed: true,
		Session: tighterWindow(accountResult.Session, keyResult.Session),
		Weekly:  tighterWindow(accountResult.Weekly, keyResult.Weekly),
	}
	mergePerMinute(&merged, accountResult, keyResult)
	return merged, nil
}

// tighterWindow picks the binding of two scopes' views of the same window: the
// configured one, or the one with less left.
//
// Only ever called on an ALLOWED check, where both sides have been charged and
// so both Remaining values are the same quantity. It used to be called on the
// refusal path too, where one side had been charged and the other had not, so
// "less remaining wins" compared a post-spend number against a pre-spend one
// and could hand a 429 the wrong scope's percentages.
func tighterWindow(a, b WindowState) WindowState {
	switch {
	case !a.Configured:
		return b
	case !b.Configured:
		return a
	case b.Remaining < a.Remaining:
		return b
	default:
		return a
	}
}

// fillWindow substitutes the fallback only where the primary scope has nothing
// to say. It never picks between two configured windows.
func fillWindow(primary, fallback WindowState) WindowState {
	if primary.Configured {
		return primary
	}
	return fallback
}

// mergePerMinute carries the per-minute limits onto an allowed result.
//
// They used to be populated on the refusal branches only, so a 200 shipped no
// x-ratelimit-*-requests headers at all and a caller with an RPM configured
// still could not pace against it until it was refused -- which is the
// complaint issue #1725 opens with. The binding scope is the one with less
// left, the same rule the long windows use.
func mergePerMinute(into *LimitResult, a, b LimitResult) {
	switch {
	case a.RequestLimit <= 0:
		into.RequestLimit, into.RequestRemaining, into.RequestResetSeconds = b.RequestLimit, b.RequestRemaining, b.RequestResetSeconds
	case b.RequestLimit <= 0 || a.RequestRemaining <= b.RequestRemaining:
		into.RequestLimit, into.RequestRemaining, into.RequestResetSeconds = a.RequestLimit, a.RequestRemaining, a.RequestResetSeconds
	default:
		into.RequestLimit, into.RequestRemaining, into.RequestResetSeconds = b.RequestLimit, b.RequestRemaining, b.RequestResetSeconds
	}
	switch {
	case a.TokenLimit <= 0:
		into.TokenLimit, into.TokenRemaining, into.TokenResetSeconds = b.TokenLimit, b.TokenRemaining, b.TokenResetSeconds
	case b.TokenLimit <= 0 || a.TokenRemaining <= b.TokenRemaining:
		into.TokenLimit, into.TokenRemaining, into.TokenResetSeconds = a.TokenLimit, a.TokenRemaining, a.TokenResetSeconds
	default:
		into.TokenLimit, into.TokenRemaining, into.TokenResetSeconds = b.TokenLimit, b.TokenRemaining, b.TokenResetSeconds
	}
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
	if l.commitLongWindows == nil {
		l.commitLongWindows = l.defaultCommitLongWindows
	}
}

// scoreScope decides one scope without charging it.
//
// The per-minute sliding windows still count on the way past: they are a
// throughput guard rather than a subscription allowance, they have behaved
// this way since v1.0, and a slot spent on a refusal is back within the
// minute. The long windows are the ones carrying a customer's paid allowance,
// so they are only SCORED here; the charge is a second pass in Check, once
// every window in every scope has admitted. The returned pendingCharges are
// what that pass applies.
func (l *Limiter) scoreScope(ctx context.Context, slidingScope string, windowPrefix string, policy *RatePolicy, anchor time.Time, anchored bool, estimatedCredits int64, billableTokens int64, freeTokens int64, now time.Time) (LimitResult, []pendingCharge, error) {
	totalTokens := billableTokens + freeTokens
	result := LimitResult{Allowed: true}

	if policy.RateLimitRPM > 0 {
		allowed, remaining, reset, err := l.runSlidingWindow(ctx, slidingWindowKeys(slidingScope, "rpm"), policy.RateLimitRPM, 1, now)
		if err != nil {
			return LimitResult{}, nil, err
		}
		result.RequestLimit = policy.RateLimitRPM
		result.RequestRemaining = maxInt(remaining, 0)
		result.RequestResetSeconds = maxInt(reset, 0)
		if !allowed {
			return LimitResult{
				Allowed:             false,
				Reason:              "request_limit_exceeded",
				RequestLimit:        policy.RateLimitRPM,
				RequestRemaining:    maxInt(remaining, 0),
				RequestResetSeconds: maxInt(reset, 0),
				RetryAfterSeconds:   maxInt(reset, 0),
				ResetAt:             now.Add(time.Duration(maxInt(reset, 0)) * time.Second),
			}, nil, nil
		}
	}

	if policy.RateLimitTPM > 0 {
		allowed, remaining, reset, err := l.runSlidingWindow(ctx, slidingWindowKeys(slidingScope, "tpm"), policy.RateLimitTPM, totalTokens, now)
		if err != nil {
			return LimitResult{}, nil, err
		}
		result.TokenLimit = policy.RateLimitTPM
		result.TokenRemaining = maxInt(remaining, 0)
		result.TokenResetSeconds = maxInt(reset, 0)
		if !allowed {
			return LimitResult{
				Allowed:           false,
				Reason:            "token_limit_exceeded",
				TokenLimit:        policy.RateLimitTPM,
				TokenRemaining:    maxInt(remaining, 0),
				TokenResetSeconds: maxInt(reset, 0),
				RetryAfterSeconds: maxInt(reset, 0),
				ResetAt:           now.Add(time.Duration(maxInt(reset, 0)) * time.Second),
			}, nil, nil
		}
	}

	score := weightedWindowScore(*policy, estimatedCredits, billableTokens, freeTokens)

	var pending []pendingCharge
	for _, window := range []struct {
		shape ratewindows.Shape
		limit int64
		state *WindowState
	}{
		{ratewindows.SessionShape(), policy.RollingFiveHourLimit, &result.Session},
		{ratewindows.WeeklyShape(), policy.WeeklyLimit, &result.Weekly},
	} {
		*window.state = WindowState{Name: window.shape.Name}

		// An unconfigured window is unlimited, and says so rather than
		// reporting a zero limit that a display would render as a full bar.
		//
		// Owner directive 2026-08-30 stands: Hive imposes no default rate
		// limit, so unset means unlimited. What issue #1725 changes is that
		// unset is now EXPLICIT everywhere it is read -- Configured false in
		// this struct, "unlimited" on every customer surface -- instead of
		// being a zero that silently skipped the check. A deliberate zero
		// allowance is not expressible on purpose and is refused by the
		// writer (control-plane validateWindowLimit), so the two meanings can
		// never collide in one column again.
		if window.limit <= 0 {
			continue
		}
		// An anchored window with no usable anchor is not evaluated at all.
		// Counting it against the epoch grid, which is what the old fallback
		// did, writes into a key the console never reads back: the enforcement
		// and the display would disagree permanently, and the counts would be
		// invisible for good. Check has already logged this.
		if window.shape.Anchored && !anchored {
			continue
		}

		familyPrefix := ratewindows.FamilyPrefix(windowPrefix, window.shape.Name)
		run, err := l.runLongWindow(ctx, familyPrefix, window.shape, anchor, window.limit, score, now)
		if err != nil {
			return LimitResult{}, nil, err
		}
		// Used and Remaining are PRE-charge on both branches, so the two
		// scopes' views of the same window are the same quantity and can be
		// compared. Check charges the admitted ones afterwards.
		*window.state = WindowState{
			Name:         window.shape.Name,
			Configured:   true,
			Limit:        window.limit,
			Used:         maxInt64(run.Used, 0),
			Remaining:    maxInt64(run.Remaining, 0),
			ResetAt:      run.ResetAt,
			ResetSeconds: secondsUntil(now, run.ResetAt),
		}
		if !run.Allowed {
			result.Allowed = false
			result.Reason = window.shape.Name + "_limit_exceeded"
			result.Window = window.shape.Name
			result.ResetAt = run.RetryAfter
			// RetryAfterSeconds, not RequestResetSeconds: that one names the
			// requests-per-minute window, and filling it with a reset four
			// hours out reads as a per-minute limiter that has lost its mind.
			result.RetryAfterSeconds = secondsUntil(now, run.RetryAfter)
			return result, nil, nil
		}
		if score > 0 {
			pending = append(pending, pendingCharge{
				key:    ratewindows.BucketKey(familyPrefix, ratewindows.Bucket(now, window.shape.EffectiveAnchor(anchor), window.shape.BucketSize)),
				score:  score,
				ttl:    window.shape.BucketSize * time.Duration(window.shape.Buckets+1),
				window: window.shape.Name,
			})
		}
	}

	return result, pending, nil
}

// pendingCharge is one long window that admitted at scoring time and has not
// been charged yet.
//
// It names its window rather than pointing at a WindowState, because the
// LimitResult it came from is returned by value and a pointer into the callee's
// local would be aimed at a dead copy.
type pendingCharge struct {
	key    string
	score  int64
	ttl    time.Duration
	window string
}

// applyCharges moves the reported state from pre-charge to post-charge, so the
// success headers describe each window as it stands after this request rather
// than as it stood before it.
func applyCharges(result *LimitResult, charges []pendingCharge) {
	for _, charge := range charges {
		switch charge.window {
		case ratewindows.Session:
			chargeWindow(&result.Session, charge.score)
		case ratewindows.Weekly:
			chargeWindow(&result.Weekly, charge.score)
		}
	}
}

func chargeWindow(state *WindowState, score int64) {
	if !state.Configured {
		return
	}
	state.Used += score
	state.Remaining = maxInt64(state.Limit-state.Used, 0)
}

// secondsUntil is the retry-after value for a reset instant, rounded up so a
// client that waits exactly this long finds the window actually open.
func secondsUntil(now, reset time.Time) int {
	if reset.IsZero() || !reset.After(now) {
		return 0
	}
	d := reset.Sub(now)
	secs := int((d + time.Second - 1) / time.Second)
	if secs < 1 {
		return 1
	}
	return secs
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

func (l *Limiter) defaultRunLongWindow(ctx context.Context, familyPrefix string, shape ratewindows.Shape, accountAnchor time.Time, limit int64, score int64, now time.Time) (longWindowResult, error) {
	if l.redis == nil || l.windowScoreScript == nil {
		return longWindowResult{}, errors.New("authz: limiter long-window script unavailable")
	}

	anchor := shape.EffectiveAnchor(accountAnchor)
	currentBucket := ratewindows.Bucket(now, anchor, shape.BucketSize)
	result, err := l.windowScoreScript.Run(
		ctx,
		l.redis,
		[]string{ratewindows.BucketKey(familyPrefix, currentBucket)},
		familyPrefix,
		currentBucket,
		shape.BucketSize.Milliseconds(),
		shape.Buckets,
		limit,
		score,
		now.UnixMilli(),
		anchor.UnixMilli(),
	).Result()
	if err != nil {
		return longWindowResult{}, fmt.Errorf("authz: run long-window limiter: %w", err)
	}

	values, ok := result.([]any)
	if !ok || len(values) != 5 {
		return longWindowResult{}, fmt.Errorf("authz: unexpected long-window result %#v", result)
	}
	numbers := make([]int64, 5)
	for i, v := range values {
		n, err := asInt64(v)
		if err != nil {
			return longWindowResult{}, err
		}
		numbers[i] = n
	}

	return longWindowResult{
		Allowed:    numbers[0] == 1,
		Remaining:  numbers[1],
		Used:       numbers[4],
		RetryAfter: now.Add(time.Duration(numbers[2]) * time.Millisecond),
		ResetAt:    now.Add(time.Duration(numbers[3]) * time.Millisecond),
	}, nil
}

// defaultCommitLongWindows charges every window that admitted, in one round
// trip. Plain INCRBY and PEXPIRE rather than a script: the scoring pass has
// already read and decided, so there is nothing left to compute, and the two
// scopes' keys live on different Redis Cluster slots, which a single script
// could not span anyway.
func (l *Limiter) defaultCommitLongWindows(ctx context.Context, charges []pendingCharge) error {
	if len(charges) == 0 {
		return nil
	}
	if l.redis == nil {
		return errors.New("authz: limiter long-window commit unavailable")
	}
	pipe := l.redis.Pipeline()
	for _, charge := range charges {
		pipe.IncrBy(ctx, charge.key, charge.score)
		pipe.PExpire(ctx, charge.key, charge.ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("authz: charge long windows: %w", err)
	}
	return nil
}

func slidingWindowKeys(scope string, metric string) []string {
	return []string{
		fmt.Sprintf("rl:{%s}:%s:current", scope, metric),
		fmt.Sprintf("rl:{%s}:%s:previous", scope, metric),
	}
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

func maxInt64(v int64, floor int64) int64 {
	if v < floor {
		return floor
	}
	return v
}

func maxInt(v int, floor int) int {
	if v < floor {
		return floor
	}
	return v
}
