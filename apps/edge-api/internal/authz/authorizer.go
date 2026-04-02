package authz

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	apierrors "github.com/hivegpt/hive/apps/edge-api/internal/errors"
)

// Authorizer performs hot-path edge authorization checks.
type Authorizer struct {
	client  *Client
	limiter *Limiter
}

// NewAuthorizer creates a new Authorizer.
func NewAuthorizer(client *Client, limiter *Limiter) *Authorizer {
	return &Authorizer{client: client, limiter: limiter}
}

func newErr(errType string, message string, code *string) *apierrors.OpenAIError {
	e := apierrors.NewError(errType, message, code)
	return &e
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
			// On limiter failure, fail open to avoid turning transient Redis problems into hard outages.
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
