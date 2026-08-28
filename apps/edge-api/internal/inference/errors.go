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
func readLimitedBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, apierrors.MaxRequestBodyBytes)
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
