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
		// All resolution failures (not found, revoked, network error mapping) manifest as invalid key.
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
