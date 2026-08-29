package inference

import (
	"fmt"
	"io"
	"log"
	"net/http"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/httpx"
)

// readLimitedBody reads r.Body up to apierrors.MaxRequestBodyBytes through
// httpx.ReadBody, and writes the caller-facing error itself. It reports
// whether the caller should continue.
//
// httpx.ReadBody caps the read with http.MaxBytesReader, which errors instead
// of silently truncating an oversized body. Before this, io.LimitReader
// truncated silently; the truncated bytes then failed json.Unmarshal and the
// caller saw a lying "Invalid request body." with no mention of size anywhere
// (issue #1250). It also refuses a declared-oversize body before reading it,
// and bounds how long the read may take (issue #1299), which matters here
// more than anywhere else in this server: see below.
//
// Shared by every inference-package endpoint that decodes a client-supplied
// JSON body (chat/completions, completions, embeddings, responses), so the
// cap and its error shape can only diverge by editing this one function.
//
// apierrors.IsTrustedBody(r.Context()) skips the cap entirely: the
// /v1/messages surface delegates here with a translated body that is
// already fully in memory and was already validated at its own ingress
// boundary, so re-capping it can only wrongly reject a client body that
// never exceeded anything (#1273 review finding 2). It skips the deadline
// with it: that body is a bytes.Reader, not a connection, so there is
// nothing to time out and nothing an adversary can hold open.
//
// This read happens before credential validation for an API-key (hk_)
// caller: the authorizer only runs inside executeSync's own Authorize step,
// downstream of every call site here. An unauthenticated caller can
// therefore make this handler buffer up to MaxRequestBodyBytes. audio and
// images validate before reading for this reason; this package does not,
// and this PR does not reorder it (a bigger, riskier change than fixing the
// body-size cap itself). What is bounded instead is the size, the duration
// and the container's memory. The ordering fix is issue #1299.
func readLimitedBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if apierrors.IsTrustedBody(r.Context()) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeInvalidBodyError(w)
			return nil, false
		}
		return body, true
	}
	body, err := httpx.ReadBody(w, r, apierrors.MaxRequestBodyBytes)
	if err != nil {
		if httpx.TooLarge(err) {
			writeRequestTooLargeError(w)
			return nil, false
		}
		writeInvalidBodyError(w)
		return nil, false
	}
	return body, true
}

func writeRequestTooLargeError(w http.ResponseWriter) {
	code := "request_too_large"
	apierrors.WriteError(w, http.StatusRequestEntityTooLarge, "invalid_request_error",
		apierrors.RequestTooLargeMessage(), &code)
}

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

// writeUnsupportedBestOfError refuses a legacy-completions request that asks
// the provider to generate several candidates server-side and hand back only
// the best one (issue #1283, review finding 5).
//
// Identical defect to n above, and the same remedy. Nothing in this package
// read best_of: the outbound body is re-marshalled from a map, so every field
// the caller sent survives byte for byte and best_of reached the provider
// intact, while the generated OpenAPI contract advertised it as supported.
//
// It costs money in both directions. Upstream it multiplies generated tokens
// against a single per-request completion ceiling, which is the settlement
// invariant completion_ceiling.go exists to hold. Downstream, a provider that
// honours it bills the caller for candidates they never receive, and one that
// drops it answers 200 with a single candidate and no indication the parameter
// vanished, which is the accept-and-silently-truncate outcome the OpenAI
// contract does not allow.
//
// ponytail: a flat refusal, not a capability lookup, exactly like the n guard
// above. Give it a provider_capabilities column the day a route can serve it.
func writeUnsupportedBestOfError(w http.ResponseWriter) {
	code := "unsupported_parameter"
	apierrors.WriteErrorWithParam(w, http.StatusBadRequest, "invalid_request_error",
		"This endpoint generates exactly one completion per request. Omit 'best_of' or set it to 1.",
		&code, "best_of")
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
