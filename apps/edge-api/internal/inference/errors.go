package inference

import (
	"fmt"
	"log"
	"net/http"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

func writeUnsupportedParamError(w http.ResponseWriter, param, model string) {
	code := "unsupported_parameter"
	msg := fmt.Sprintf("Model does not support parameter: %s. Choose an alias with tool-calling capability.", param)
	if model != "" {
		msg = fmt.Sprintf("Model '%s' does not support parameter: %s. Choose an alias with tool-calling capability.", model, param)
	}
	apierrors.WriteErrorWithParam(w, http.StatusBadRequest, "invalid_request_error", msg, &code, param)
}

// writeUnsupportedChoiceCountError refuses a request asking for a number of
// choices this gateway cannot serve (issue #1283).
//
// n was declared on the request structs and read by nothing: it was forwarded
// upstream, ignored, and one choice came back with HTTP 200 and no indication
// the parameter had been dropped. Accept-and-silently-truncate is the one
// outcome the OpenAI contract does not allow, because a caller has no way to
// tell a parameter that was honoured from one that vanished.
//
// Rejecting rather than honouring, because no route in this catalog can serve
// n > 1: the free pool's Groq members accept only n=1 on their
// OpenAI-compatible surface, and OpenRouter does not implement the parameter
// across the pool either. Honouring it would also multiply generated tokens
// against a single per-request max_tokens ceiling, which is the settlement
// invariant in completion_ceiling.go.
//
// Provider-blind by construction: the message names the parameter and nothing
// about who serves the request.
//
// ponytail: a flat refusal, not a capability lookup. Give it a
// provider_capabilities column the day a route can actually serve n > 1.
func writeUnsupportedChoiceCountError(w http.ResponseWriter) {
	code := "unsupported_parameter"
	apierrors.WriteErrorWithParam(w, http.StatusBadRequest, "invalid_request_error",
		"This endpoint generates exactly one choice per request. Omit 'n' or set it to 1, and send multiple requests if you need multiple completions.",
		&code, "n")
}

// unsupportedChoiceCount reports whether the caller asked for a choice count
// this gateway cannot serve. Absent and 1 are servable; everything else,
// including the 0 and the negatives OpenAI itself rejects, is not.
func unsupportedChoiceCount(n *int) bool {
	return n != nil && *n != 1
}

func writeModelNotFoundError(w http.ResponseWriter, model string) {
	code := "model_not_found"
	log.Printf("inference: model_not_found via routing layer model=%q", model)
	apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error",
		fmt.Sprintf("The model `%s` does not exist or you do not have access to it.", model), &code)
}

// writeModelNotEntitledError reports a route-selection failure caused by the
// requesting API key's tenant not being entitled to the alias (admin hid it,
// or it is restricted and the tenant holds no grant). Reuses the same
// "does not exist or you do not have access to it" phrasing as
// writeModelNotFoundError so a caller cannot distinguish "unknown model" from
// "model exists but you're not entitled" -- an administrative policy verdict,
// not a resource lookup, so 403 rather than 404.
func writeModelNotEntitledError(w http.ResponseWriter, model string) {
	code := "model_not_found"
	apierrors.WriteError(w, http.StatusForbidden, "invalid_request_error",
		fmt.Sprintf("The model `%s` does not exist or you do not have access to it.", model), &code)
}

// writeAccountNotProvisionedError reports an API key whose account has no
// resolvable tenant (public.tenant_billing_accounts has no row for it, D-030).
// Fails closed: without a tenant the entitlement check cannot run at all, so
// this is a 403 refusal rather than a fallback to unfiltered access.
func writeAccountNotProvisionedError(w http.ResponseWriter) {
	code := "account_not_provisioned"
	apierrors.WriteError(w, http.StatusForbidden, "invalid_request_error",
		"This API key's account is not yet linked to a workspace. Contact support to complete account setup.", &code)
}

func writeMissingFieldError(w http.ResponseWriter, field string) {
	apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
		fmt.Sprintf("Missing required parameter: '%s'.", field), nil)
}

func writeInvalidBodyError(w http.ResponseWriter) {
	apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
		"Invalid request body.", nil)
}
