package authz

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/sakibsadmanshajib/hive/packages/ratewindows"
)

// Authorizer performs hot-path edge authorization checks.
type Authorizer struct {
	client  *Client
	limiter *Limiter

	// failOpen controls behavior when the rate limiter backend (Redis) cannot
	// be evaluated. Default false = fail closed (deny with a retryable 429) so a
	// backend outage cannot silently disable abuse controls (#51). An operator
	// may opt into fail-open for dev/local via WithFailOpen.
	failOpen bool
}

// AuthorizerOption configures an Authorizer.
type AuthorizerOption func(*Authorizer)

// WithFailOpen sets the limiter-degraded policy. failOpen=true admits requests
// when the limiter backend errors (dev/local only); the production default is
// fail closed. See #51.
func WithFailOpen(failOpen bool) AuthorizerOption {
	return func(a *Authorizer) { a.failOpen = failOpen }
}

// NewAuthorizer creates a new Authorizer. The default limiter-degraded policy
// is fail closed; pass WithFailOpen(true) to opt into fail-open.
func NewAuthorizer(client *Client, limiter *Limiter, opts ...AuthorizerOption) *Authorizer {
	a := &Authorizer{client: client, limiter: limiter}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func newErr(errType string, message string, code *string) *apierrors.OpenAIError {
	e := apierrors.NewError(errType, message, code)
	return &e
}

// internalTokenRejectionLogInterval bounds how often
// logInternalTokenRejectionOncePerMinute actually writes to the log. This
// condition is a permanent misconfiguration by construction (PR #903
// second-pass security review): it fires on every request for as long as it
// lasts, so logging it unthrottled turns a config error into a
// disk-filling loop. Loud the first time, then at most once per interval --
// still frequent enough that an operator watching logs live sees it
// immediately, without flooding a log aggregator over a sustained outage.
const internalTokenRejectionLogInterval = time.Minute

// lastInternalTokenRejectionLogUnix holds the unix-seconds timestamp of the
// last time logInternalTokenRejectionOncePerMinute actually logged. Package-
// level and shared across every Authorizer instance deliberately: the
// condition it throttles (a misconfigured shared secret) is process-wide,
// not per-instance, so there is exactly one meaningful rate to enforce.
var lastInternalTokenRejectionLogUnix atomic.Int64

// logInternalTokenRejectionOncePerMinute logs a rejected-internal-token
// CONFIGURATION ERROR at most once per internalTokenRejectionLogInterval.
func logInternalTokenRejectionOncePerMinute(err error) {
	now := time.Now().Unix()
	last := lastInternalTokenRejectionLogUnix.Load()
	if now-last < int64(internalTokenRejectionLogInterval.Seconds()) {
		return
	}
	if !lastInternalTokenRejectionLogUnix.CompareAndSwap(last, now) {
		return // another goroutine just logged it instead
	}
	log.Printf("authz: CONFIGURATION ERROR key resolution is permanently broken: control-plane's internal-token check rejected edge-api's own request (CONTROL_PLANE_INTERNAL_TOKEN mismatch or unset on control-plane, or an intermediary in front of it with its own authentication) -- this will NOT self-resolve by waiting or retrying, and this message is rate-limited to once per %s while the condition persists err=%v", internalTokenRejectionLogInterval, err)
}

// AuthzError carries a structured authorization failure (the OpenAI error
// envelope plus any rate-metadata headers) through adapter boundaries that
// would otherwise flatten it to a generic error. Handlers type-assert this to
// preserve the correct status — notably a retryable degraded-limiter 429 with
// retry-after rather than a non-retryable 401 (#51).
type AuthzError struct {
	OpenAIErr *apierrors.OpenAIError
	Headers   map[string]string
}

func (e *AuthzError) Error() string {
	if e == nil || e.OpenAIErr == nil {
		return "authz: unauthorized"
	}
	return e.OpenAIErr.Error.Message
}

// AsAuthzError reports whether err is an *AuthzError and returns it.
func AsAuthzError(err error) (*AuthzError, bool) {
	var ae *AuthzError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}

// Authorize validates a request against the AuthSnapshot system.
// It maps domain errors to OpenAI-compatible API responses.
func (a *Authorizer) Authorize(ctx context.Context, authHeader string, aliasID string, estimatedCredits, billableTokens, freeTokens int64) (AuthSnapshot, map[string]string, *apierrors.OpenAIError) {
	rawToken := strings.TrimPrefix(authHeader, "Bearer ")
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		code := "invalid_api_key"
		return AuthSnapshot{}, nil, newErr(
			"invalid_request_error",
			"You didn't provide an API key. You need to provide your API key in an Authorization header using Bearer auth (i.e. Authorization: Bearer YOUR_KEY).",
			&code,
		)
	}

	snapshot, err := a.client.Resolve(ctx, rawToken)
	if err != nil {
		// All resolution failures (not found, revoked, control-plane/network
		// error) manifest to the CALLER as the same generic invalid-key
		// message, deliberately: distinguishing them client-side would let a
		// caller enumerate which keys exist. Server-side, collapsing them the
		// same way is a bug, not a feature -- log the real cause (err already
		// carries the resolve-path outcome, e.g. control-plane status code
		// for not-found/revoked vs a transport error) so an outage is
		// diagnosable from logs instead of guesswork across reruns.
		if errors.Is(err, ErrInternalTokenRejected) {
			// Loud and distinct on purpose (PR #903 security review): this is
			// a permanent misconfiguration, not a transient condition, and
			// will keep failing every request until an operator fixes it --
			// it must not blend into ordinary cold-start/timeout log noise
			// that an on-call engineer would reasonably wait out.
			//
			// Attribution is deliberately not pinned solely on
			// CONTROL_PLANE_INTERNAL_TOKEN (second-pass security review):
			// RequireInternalToken on control-plane itself is the only
			// authenticator on this route in the shipped topology, but
			// CONTROL_PLANE_URL is operator-supplied, and anything sitting in
			// front of it with its own auth (a proxy, an access gateway
			// rejecting a service token) answers the identical 401 here. The
			// message names control-plane's own check as the likely cause
			// without asserting it is the only possible one.
			//
			// Rate-limited to once per minute rather than once per request:
			// this condition is permanent by construction, so every request
			// hits it for as long as it lasts, and an unthrottled multi-line
			// CONFIGURATION ERROR message on every single request is a
			// disk-filling loop bolted onto an auth failure.
			logInternalTokenRejectionOncePerMinute(err)
		} else {
			log.Printf("authz: key resolution failed err=%v", err)
		}
		if errors.Is(err, ErrUpstreamUnavailable) {
			// The resolve call never reached a verdict on this key (transport
			// failure, timeout, canceled context, a control-plane 5xx, or a
			// rejected internal token) -- answer with a retryable,
			// provider-blind 503 rather than the permanent, non-retryable 401
			// a genuinely invalid key gets. A 401 on a valid credential reads
			// as "this key is wrong" and sends the caller to rotate it; that
			// is worse than a slow, honest failure.
			//
			// Explicit judgment on the resulting 401-vs-503 split, per PR #903
			// security review (corrected from an earlier, overconfident
			// version of this comment that claimed no oracle existed at all --
			// that claim was wrong and has been retracted): this IS a narrow,
			// real information leak, not eliminated by this change.
			// control-plane's ResolveSnapshot (apikeys/service.go) fails fast
			// at GetPolicyByTokenHash for a key that does not exist, before
			// touching ListAllAliases/GetBudgetWindow/GetAccountRatePolicy/
			// GetKeyRatePolicy/GetTenantIDByAccountID; a key that DOES exist
			// proceeds through all of those, so during a degraded window
			// (connection-pool exhaustion, request cancellation -- exactly
			// what the 2026-08-14 incident's "get key rate policy" and
			// "resolve tenant for account: context canceled" log lines were)
			// an existing key is more likely to surface as 503 than a
			// nonexistent one, which reliably 401s fast. Accepted rather than
			// reverted: exploiting it requires riding an already-degraded
			// control-plane window an attacker cannot RELIABLY induce from
			// here (worded precisely per second-pass security review: this
			// control-plane runs against a small shared session-mode pool
			// that ordinary load alone has previously been enough to
			// saturate, so a caller with nothing but valid API access could
			// plausibly help manufacture that window rather than only wait
			// for one -- weakly attacker-triggerable, not merely
			// opportunistic; "cannot induce" overstated that and has been
			// corrected). It still yields only a probabilistic existence
			// signal and no access, and the alternative -- collapsing 503
			// back into 401 -- reintroduces the certain, severe cost this PR
			// exists to remove (a valid credential told it is invalid) to
			// close a narrow, low-value leak. Follow-up tracked as issue
			// #904 rather than left only in this comment: rate-limiting or
			// aggregating repeated resolve failures per source would shrink
			// the window in which this is observable.
			code := "upstream_unavailable"
			return AuthSnapshot{}, map[string]string{"retry-after": "5"}, newErr(
				"api_error",
				"The authorization service is temporarily unavailable. Please retry.",
				&code,
			)
		}
		code := "invalid_api_key"
		return AuthSnapshot{}, nil, newErr(
			"invalid_request_error",
			"Incorrect API key provided.",
			&code,
		)
	}

	check := CheckAccess(snapshot, aliasID, estimatedCredits)
	if !check.Allowed {
		switch check.DenyCode {
		case "invalid_api_key":
			// check.DenyMsg already differentiates revoked/disabled/expired
			// for the client message below; log it too so the same cause is
			// visible server-side without grepping response bodies.
			log.Printf("authz: access denied alias=%q reason=%q", aliasID, check.DenyMsg)
			code := "invalid_api_key"
			return AuthSnapshot{}, nil, newErr(
				"invalid_request_error",
				"Incorrect API key provided: "+check.DenyMsg,
				&code,
			)
		case "model_not_allowed":
			log.Printf("authz: model_not_allowed alias=%q allow_all=%v allowed_aliases=%v", aliasID, snapshot.AllowAllModels, snapshot.AllowedAliases)
			code := "model_not_found"
			return AuthSnapshot{}, nil, newErr(
				"invalid_request_error",
				"The model `"+aliasID+"` does not exist or you do not have access to it.",
				&code,
			)
		case "budget_exceeded":
			code := "insufficient_quota"
			return AuthSnapshot{}, nil, newErr(
				"insufficient_quota",
				"You exceeded your current quota, please check your plan and billing details.",
				&code,
			)
		default:
			code := "invalid_api_key"
			return AuthSnapshot{}, nil, newErr(
				"invalid_request_error",
				"Access denied.",
				&code,
			)
		}
	}

	var successHeaders map[string]string
	if a.limiter != nil {
		limitResult, err := a.limiter.Check(ctx, snapshot, aliasID, estimatedCredits, billableTokens, freeTokens)
		if err != nil {
			// #51: the limiter backend (Redis) could not be evaluated. Emit a
			// structured, operator-visible signal at the request boundary so a
			// degraded limiter is never silent.
			log.Printf("authz: rate limiter degraded account=%q key=%q fail_open=%v err=%v",
				snapshot.AccountID, snapshot.KeyID, a.failOpen, err)
			if !a.failOpen {
				// Fail closed (production default): deny with a retryable 429
				// rather than admit unmetered traffic. Message is provider-blind
				// — no backend/internal detail leaks to the customer.
				code := "rate_limit_exceeded"
				return AuthSnapshot{}, map[string]string{"retry-after": "5"}, newErr(
					"rate_limit_error",
					"Rate limiting is temporarily unavailable. Please retry in a few seconds.",
					&code,
				)
			}
			// Fail open: explicitly enabled by operator (dev/local). Admit
			// despite the degraded limiter.
		} else if !limitResult.Allowed {
			return AuthSnapshot{}, RateLimitHeaders(limitResult), rateLimitError(limitResult)
		} else {
			// Success headers. A limit nobody can see until they hit it is
			// the defect issue #1725 was filed for; every caller of Authorize
			// that holds a ResponseWriter applies these.
			successHeaders = RateLimitHeaders(limitResult)
		}
	}

	return snapshot, successHeaders, nil
}

// rateLimitMessage is the sentence a customer reads on a refusal. It names the
// window that ran out and the instant it comes back, because "Please try again
// later" is indistinguishable from a broken product, which is exactly how this
// was reported (issue #1725).
//
// Percentages and times only, never a currency figure or a credit balance: the
// allowance is a proportion the customer spends, and D-070 keeps money off
// every surface but an invoice.
func rateLimitMessage(result LimitResult) string {
	switch result.Reason {
	case "request_limit_exceeded":
		return fmt.Sprintf("Rate limit reached for requests. Limit: %d per minute.%s", result.RequestLimit, resetClause(result))
	case "token_limit_exceeded":
		return fmt.Sprintf("Rate limit reached for tokens. Limit: %d per minute.%s", result.TokenLimit, resetClause(result))
	case "session_limit_exceeded":
		return windowMessage(result.Session,
			"You have used all of your session allowance. Hive measures a session over a rolling five hour window.",
			"is larger than the %d%% of your session allowance that is left. Hive measures a session over a rolling five hour window.",
		) + resetClause(result)
	case "weekly_limit_exceeded":
		return windowMessage(result.Weekly,
			"You have used all of your weekly allowance. It restores in full on your account's weekly reset.",
			"is larger than the %d%% of your weekly allowance that is left. It restores in full on your account's weekly reset.",
		) + resetClause(result)
	default:
		return "Rate limit reached for your current usage window." + resetClause(result)
	}
}

// windowMessage distinguishes an exhausted allowance from one that simply
// cannot fit this request.
//
// Both refuse, and saying "you have used all of your allowance" for the second
// is false: a caller with most of the window left, sending one request larger
// than the remainder, would be told they had spent everything and would go
// looking for usage that is not there. Seen live during the issue #1725 proof
// capture, where a first request against a fresh window refused with nothing
// consumed at all.
func windowMessage(window WindowState, exhausted string, oversizedFormat string) string {
	remaining := window.RemainingPercent()
	if !window.Configured || remaining <= 0 {
		return exhausted
	}
	return "This request " + fmt.Sprintf(oversizedFormat, remaining)
}

// resetClause renders the reset instant, in UTC and in RFC3339, plus a
// human-readable interval. Empty when the limiter could not produce a real
// reset, rather than inventing one.
func resetClause(result LimitResult) string {
	if result.ResetAt.IsZero() {
		return ""
	}
	return fmt.Sprintf(" It resets at %s (in %s).",
		result.ResetAt.UTC().Format(time.RFC3339),
		humanizeDuration(retryAfterSeconds(result)))
}

// humanizeDuration renders whole seconds as the coarsest sensible unit.
func humanizeDuration(seconds int) string {
	switch {
	case seconds <= 0:
		return "under a minute"
	case seconds < 60:
		return fmt.Sprintf("%d seconds", seconds)
	case seconds < 3600:
		return pluralize(seconds/60, "minute")
	case seconds < 86400:
		return pluralize(seconds/3600, "hour")
	default:
		return pluralize(seconds/86400, "day")
	}
}

func pluralize(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// rateLimitError builds the wire error for a refusal.
//
// A long-window refusal carries its own code, so an allowance refusal, a
// per-minute throughput refusal and the spend-cap refusal in internal/limits
// are three outcomes a caller can branch on rather than one indistinguishable
// string. The per-minute pair deliberately keeps the historical
// rate_limit_exceeded: those two have been reachable in production since v1.0
// and every OpenAI-compatible SDK matches on that exact code, whereas no
// caller anywhere has ever seen a long-window refusal (no account had a window
// configured, issue #1725), so there is no contract to keep for those.
func rateLimitError(result LimitResult) *apierrors.OpenAIError {
	code := "rate_limit_exceeded"
	switch result.Reason {
	case "session_limit_exceeded", "weekly_limit_exceeded":
		code = result.Reason
	}
	err := apierrors.NewError("rate_limit_error", rateLimitMessage(result), &code)
	if !result.ResetAt.IsZero() {
		err.Error.ResetAt = result.ResetAt.UTC().Format(time.RFC3339)
	}
	err.Error.LimitWindow = result.Window
	return &err
}

// RateLimitHeaders renders the limiter result as response headers.
//
// Exported and emitted on SUCCESS as well as on a refusal (issue #1725): a
// caller cannot pace itself against a limit it only learns about by hitting it,
// and headers that appear solely on a 429 are the reason no Hive surface could
// show a consumption figure.
//
// Both spellings ship: the OpenAI style x-ratelimit-* set every OpenAI SDK
// already reads, and the IETF RateLimit-* aliases newer clients look for.
func RateLimitHeaders(result LimitResult) map[string]string {
	headers := make(map[string]string)

	if result.RequestLimit > 0 {
		headers["x-ratelimit-limit-requests"] = strconv.Itoa(result.RequestLimit)
		headers["x-ratelimit-remaining-requests"] = strconv.Itoa(maxInt(result.RequestRemaining, 0))
	}
	// Only when a per-minute request limit is the thing being reported. This
	// header names the requests-per-minute window, and a long-window refusal
	// used to fill it with a reset four hours out, which reads as a per-minute
	// limiter that has lost its mind.
	if result.RequestLimit > 0 && result.RequestResetSeconds > 0 {
		headers["x-ratelimit-reset-requests"] = strconv.Itoa(result.RequestResetSeconds)
	}
	if result.TokenLimit > 0 {
		headers["x-ratelimit-limit-tokens"] = strconv.Itoa(result.TokenLimit)
		headers["x-ratelimit-remaining-tokens"] = strconv.Itoa(maxInt(result.TokenRemaining, 0))
	}
	if result.TokenResetSeconds > 0 {
		headers["x-ratelimit-reset-tokens"] = strconv.Itoa(result.TokenResetSeconds)
	}

	// Long windows ship as PERCENTAGES, never as the raw allowance. The
	// allowance is a credit score, credits convert to dollars by a constant
	// the console publishes, and a subscription's internal credit value is
	// confidential (D-068); a percentage says everything a caller needs for
	// pacing and discloses none of it (D-070).
	var policies []string
	for _, window := range []WindowState{result.Session, result.Weekly} {
		if !window.Configured {
			continue
		}
		headers["x-ratelimit-"+window.Name+"-used-percent"] = strconv.Itoa(window.UsedPercent())
		headers["x-ratelimit-"+window.Name+"-remaining-percent"] = strconv.Itoa(window.RemainingPercent())
		headers["x-ratelimit-"+window.Name+"-reset"] = strconv.Itoa(windowResetSeconds(window))
		headers["x-ratelimit-"+window.Name+"-reset-at"] = window.ResetAt.UTC().Format(time.RFC3339)
		policies = append(policies, fmt.Sprintf("%q;q=100;w=%d", window.Name, windowSeconds(window.Name)))
	}

	// IETF aliases describe the binding window: the one that refused, or the
	// tightest configured one. Its quota unit is percent of allowance, which
	// the accompanying RateLimit-Policy declares as q=100.
	if binding, ok := bindingWindow(result); ok {
		headers["ratelimit-limit"] = "100"
		headers["ratelimit-remaining"] = strconv.Itoa(binding.RemainingPercent())
		headers["ratelimit-reset"] = strconv.Itoa(windowResetSeconds(binding))
	} else if result.RequestLimit > 0 {
		headers["ratelimit-limit"] = strconv.Itoa(result.RequestLimit)
		headers["ratelimit-remaining"] = strconv.Itoa(maxInt(result.RequestRemaining, 0))
		headers["ratelimit-reset"] = strconv.Itoa(maxInt(result.RequestResetSeconds, 0))
		policies = append(policies, fmt.Sprintf("%q;q=%d;w=60", "requests", result.RequestLimit))
	}
	if len(policies) > 0 {
		headers["ratelimit-policy"] = strings.Join(policies, ", ")
	}

	if retryAfter := retryAfterSeconds(result); retryAfter > 0 && !result.Allowed {
		headers["retry-after"] = strconv.Itoa(retryAfter)
	}

	return headers
}

// retryAfterSeconds is how long the caller is told to wait.
//
// RetryAfterSeconds first, because a long-window refusal carries its wait
// there rather than in RequestResetSeconds: that field names the
// requests-per-minute window, and the per-minute headers are now emitted on
// success too, so a session refusal writing into it would both forge a
// per-minute reset and lose the real one.
func retryAfterSeconds(result LimitResult) int {
	if result.RetryAfterSeconds > 0 {
		return result.RetryAfterSeconds
	}
	if result.RequestResetSeconds > 0 {
		return result.RequestResetSeconds
	}
	return result.TokenResetSeconds
}

// bindingWindow is the long window the IETF aliases describe.
func bindingWindow(result LimitResult) (WindowState, bool) {
	if result.Window != "" {
		if result.Window == result.Session.Name && result.Session.Configured {
			return result.Session, true
		}
		if result.Window == result.Weekly.Name && result.Weekly.Configured {
			return result.Weekly, true
		}
	}
	switch {
	case result.Session.Configured && result.Weekly.Configured:
		if result.Weekly.Remaining < result.Session.Remaining {
			return result.Weekly, true
		}
		return result.Session, true
	case result.Session.Configured:
		return result.Session, true
	case result.Weekly.Configured:
		return result.Weekly, true
	}
	return WindowState{}, false
}

func windowResetSeconds(window WindowState) int {
	return maxInt(window.ResetSeconds, 0)
}

func windowSeconds(name string) int {
	if name == ratewindows.Weekly {
		return int(ratewindows.WeeklyBucketSize / time.Second)
	}
	return int(ratewindows.SessionBucketSize) / int(time.Second) * ratewindows.SessionBucketCount
}
