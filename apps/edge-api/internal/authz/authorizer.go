package authz

import (
	"context"
	"strings"

	apierrors "github.com/hivegpt/hive/apps/edge-api/internal/errors"
)

// Authorizer performs hot-path edge authorization checks.
type Authorizer struct {
	client *Client
}

// NewAuthorizer creates a new Authorizer.
func NewAuthorizer(client *Client) *Authorizer {
	return &Authorizer{client: client}
}

// Authorize validates a request against the AuthSnapshot system.
// It maps domain errors to OpenAI-compatible API responses.
func (a *Authorizer) Authorize(ctx context.Context, authHeader string, aliasID string) (AuthSnapshot, *apierrors.OpenAIError) {
	rawToken := strings.TrimPrefix(authHeader, "Bearer ")
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		code := "invalid_api_key"
		return AuthSnapshot{}, apierrors.NewOpenAIError(
			"invalid_request_error",
			"You didn't provide an API key. You need to provide your API key in an Authorization header using Bearer auth (i.e. Authorization: Bearer YOUR_KEY).",
			&code,
		)
	}

	snapshot, err := a.client.Resolve(ctx, rawToken)
	if err != nil {
		// All resolution failures (not found, revoked, network error mapping) manifest as invalid key.
		code := "invalid_api_key"
		return AuthSnapshot{}, apierrors.NewOpenAIError(
			"invalid_request_error",
			"Incorrect API key provided.",
			&code,
		)
	}

	check := CheckAccess(snapshot, aliasID)
	if !check.Allowed {
		switch check.DenyCode {
		case "invalid_api_key":
			code := "invalid_api_key"
			return AuthSnapshot{}, apierrors.NewOpenAIError(
				"invalid_request_error",
				"Incorrect API key provided: "+check.DenyMsg,
				&code,
			)
		case "model_not_allowed":
			code := "model_not_found"
			return AuthSnapshot{}, apierrors.NewOpenAIError(
				"invalid_request_error",
				"The model `"+aliasID+"` does not exist or you do not have access to it.",
				&code,
			)
		case "budget_exceeded":
			code := "insufficient_quota"
			return AuthSnapshot{}, apierrors.NewOpenAIError(
				"insufficient_quota",
				"You exceeded your current quota, please check your plan and billing details.",
				&code,
			)
		default:
			code := "invalid_api_key"
			return AuthSnapshot{}, apierrors.NewOpenAIError(
				"invalid_request_error",
				"Access denied.",
				&code,
			)
		}
	}

	return snapshot, nil
}
