package authz

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
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
			// a permanent misconfiguration (CONTROL_PLANE_INTERNAL_TOKEN
			// mismatch), not a transient condition, and will keep failing
			// every request until an operator fixes the token on one or both
			// sides -- it must not blend into ordinary cold-start/timeout log
			// noise that an on-call engineer would reasonably wait out.
			log.Printf("authz: CONFIGURATION ERROR key resolution is permanently broken: control-plane rejected edge-api's own internal service token (CONTROL_PLANE_INTERNAL_TOKEN mismatch or unset) -- this will NOT self-resolve by waiting or retrying err=%v", err)
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
			// control-plane window an attacker cannot induce from here, it
			// yields only a probabilistic existence signal and no access, and
			// the alternative -- collapsing 503 back into 401 -- reintroduces
			// the certain, severe cost this PR exists to remove (a valid
			// credential told it is invalid) to close a narrow, low-value
			// leak. Follow-up worth doing separately, not blocking here:
			// rate-limiting or aggregating repeated resolve failures per
			// source would shrink the window in which this is observable.
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
			code := "rate_limit_exceeded"
			return AuthSnapshot{}, rateLimitHeaders(limitResult), newErr(
				"rate_limit_error",
				rateLimitMessage(limitResult),
				&code,
			)
		}
	}

	return snapshot, nil, nil
}

func rateLimitMessage(result LimitResult) string {
	switch result.Reason {
	case "request_limit_exceeded":
		return fmt.Sprintf("Rate limit reached for requests. Limit: %d / min. Please try again in a little while.", result.RequestLimit)
	case "token_limit_exceeded":
		return fmt.Sprintf("Rate limit reached for tokens. Limit: %d / min. Please try again in a little while.", result.TokenLimit)
	default:
		return "Rate limit reached for your current quota window. Please try again later."
	}
}

func rateLimitHeaders(result LimitResult) map[string]string {
	headers := make(map[string]string)

	if result.RequestLimit > 0 {
		headers["x-ratelimit-limit-requests"] = strconv.Itoa(result.RequestLimit)
		headers["x-ratelimit-remaining-requests"] = strconv.Itoa(maxInt(result.RequestRemaining, 0))
	}
	if result.RequestResetSeconds > 0 {
		headers["x-ratelimit-reset-requests"] = strconv.Itoa(result.RequestResetSeconds)
	}
	if result.TokenLimit > 0 {
		headers["x-ratelimit-limit-tokens"] = strconv.Itoa(result.TokenLimit)
		headers["x-ratelimit-remaining-tokens"] = strconv.Itoa(maxInt(result.TokenRemaining, 0))
	}
	if result.TokenResetSeconds > 0 {
		headers["x-ratelimit-reset-tokens"] = strconv.Itoa(result.TokenResetSeconds)
	}

	retryAfter := result.RequestResetSeconds
	if retryAfter <= 0 {
		retryAfter = result.TokenResetSeconds
	}
	if retryAfter > 0 {
		headers["retry-after"] = strconv.Itoa(retryAfter)
	}

	return headers
}
