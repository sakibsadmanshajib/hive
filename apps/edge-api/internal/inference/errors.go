package inference

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// readLimitedBody reads r.Body up to apierrors.MaxRequestBodyBytes via
// http.MaxBytesReader, which errors instead of silently truncating an
// oversized body. Before this, io.LimitReader truncated silently; the
// truncated bytes then failed json.Unmarshal and the caller saw a lying
// "Invalid request body." with no mention of size anywhere (issue #1250).
// Shared by every inference-package endpoint that decodes a client-supplied
// JSON body (chat/completions, completions, embeddings, responses), so the
// cap and its error shape can only diverge by editing this one function.
//
// apierrors.IsTrustedBody(r.Context()) skips the cap entirely: the
// /v1/messages surface delegates here with a translated body that is
// already fully in memory and was already validated at its own ingress
// boundary, so re-capping it can only wrongly reject a client body that
// never exceeded anything (#1273 review finding 2).
//
// This read happens before credential validation for an API-key (hk_)
// caller: the authorizer only runs inside executeSync's own Authorize step,
// downstream of every call site here. An unauthenticated caller can
// therefore make this handler buffer up to MaxRequestBodyBytes. audio and
// images validate before reading for this reason; this package does not,
// and this PR does not reorder it (a bigger, riskier change than fixing the
// body-size cap itself). The ContentLength pre-check above mitigates the
// common case (a declared-oversize body is rejected at ~0 bytes buffered
// rather than up to the cap), and the outer http.MaxBytesHandler in
// cmd/server/main.go already allows a much larger pre-auth body on other
// routes, so this is a real but bounded, pre-existing exposure, not one
// this PR meaningfully worsens.
func readLimitedBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if !apierrors.IsTrustedBody(r.Context()) {
		// Reject a declared oversize body before reading anything, mirroring
		// auth/owui_unwrap.go's ContentLength pre-check. This is a memory
		// optimisation, not an error-delivery fix: it bounds the server's
		// peak buffering for a declared-oversize body instead of reading up
		// to the cap before erroring, but the client sees the 413 no later
		// (often earlier), so it does not make an honest error any more
		// reachable, and ContentLength is -1 when unknown (chunked), which
		// fails this comparison and falls through to MaxBytesReader below, a
		// no-op for that transfer encoding.
		if r.ContentLength > apierrors.MaxRequestBodyBytes {
			writeRequestTooLargeError(w)
			return nil, false
		}
		r.Body = http.MaxBytesReader(w, r.Body, apierrors.MaxRequestBodyBytes)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
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
