package errors

import (
	"encoding/json"
	"net/http"
	"strings"
)

// OpenAIError is the top-level error envelope returned by OpenAI-compatible APIs.
type OpenAIError struct {
	Error OpenAIErrorBody `json:"error"`
}

// OpenAIErrorBody contains the error details inside the envelope.
//
// ResetAt and LimitWindow are Hive additions, present only on a rate-limit
// refusal and omitted everywhere else, so the envelope an OpenAI SDK parses is
// unchanged for every other error. They exist because a refusal that names no
// window and no reset is indistinguishable from an outage (issue #1725), and a
// caller should not have to parse English out of Message to retry correctly.
type OpenAIErrorBody struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
	// ResetAt is RFC3339 UTC.
	ResetAt string `json:"reset_at,omitempty"`
	// LimitWindow is "session" or "weekly" for a long-window refusal.
	LimitWindow string `json:"limit_window,omitempty"`
}

// NewError creates a new OpenAIError with the given type, message, and optional code.
// Param is always nil (set separately if needed).
func NewError(errType string, message string, code *string) OpenAIError {
	return OpenAIError{
		Error: OpenAIErrorBody{
			Message: message,
			Type:    errType,
			Param:   nil,
			Code:    code,
		},
	}
}

// WriteError writes an OpenAI-style error response with the given HTTP status code.
func WriteError(w http.ResponseWriter, httpStatus int, errType string, message string, code *string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(NewError(errType, message, code))
}

// WriteErrorWithParam writes an OpenAI-style error response that includes the
// specific parameter name that caused the error in the "param" field. This is
// used for unsupported_parameter errors so SDK callers know which field to fix.
func WriteErrorWithParam(w http.ResponseWriter, httpStatus int, errType string, message string, code *string, param string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	body := OpenAIError{
		Error: OpenAIErrorBody{
			Message: message,
			Type:    errType,
			Param:   &param,
			Code:    code,
		},
	}
	json.NewEncoder(w).Encode(body)
}

// WriteErrorBody writes an already-built error body, preserving every field on
// it. WriteError rebuilds the body from four arguments and so drops the
// rate-limit metadata; this is the path for an error that carries any.
func WriteErrorBody(w http.ResponseWriter, httpStatus int, body OpenAIErrorBody) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(OpenAIError{Error: body})
}

// ApplyHeaders sets each non-blank header on w. Exported for the success path:
// rate-limit headers now ship on a 200 as well as a 429, and the handlers that
// do that are outside this package.
func ApplyHeaders(w http.ResponseWriter, headers map[string]string) {
	applyHeaders(w, headers)
}

// applyHeaders sets each non-blank header on w. Shared by every error path
// that carries retry metadata, so a header only needs handling here once.
func applyHeaders(w http.ResponseWriter, headers map[string]string) {
	for key, value := range headers {
		if strings.TrimSpace(value) == "" {
			continue
		}
		w.Header().Set(key, value)
	}
}

// WriteRateLimitError writes a 429 OpenAI-style error with retry metadata headers.
func WriteRateLimitError(w http.ResponseWriter, message string, code *string, headers map[string]string) {
	applyHeaders(w, headers)
	WriteError(w, http.StatusTooManyRequests, "rate_limit_error", message, code)
}

// WriteAuthFailure writes the OpenAI-compatible response for an authorization
// failure, mapping the error to the correct HTTP status and preserving rate
// metadata headers. It is the single source of truth for translating an
// authz failure into a wire response — the inference hot-path and every media/
// file/batch handler route through it so a degraded-limiter 429 (retryable,
// with retry-after) is never collapsed into a non-retryable 401 (#51).
func WriteAuthFailure(w http.ResponseWriter, oerr *OpenAIError, headers map[string]string) {
	if oerr == nil {
		code := "invalid_api_key"
		WriteError(w, http.StatusUnauthorized, "invalid_request_error", "Invalid API key.", &code)
		return
	}
	// Switched on TYPE, not on the exact code string. Issue #1725 split one
	// rate-limit code into several (session_limit_exceeded,
	// weekly_limit_exceeded, and the existing per-minute pair), and a code
	// match would have quietly answered 401 to every one of the new ones: a
	// customer over their allowance being told their API key is wrong.
	if oerr.Error.Type == "rate_limit_error" {
		applyHeaders(w, headers)
		WriteErrorBody(w, http.StatusTooManyRequests, oerr.Error)
		return
	}
	status := http.StatusUnauthorized
	switch {
	case oerr.Error.Type == "insufficient_quota":
		status = http.StatusTooManyRequests
	case oerr.Error.Code != nil && *oerr.Error.Code == "model_not_found":
		status = http.StatusNotFound
	case oerr.Error.Code != nil && *oerr.Error.Code == "upstream_unavailable":
		// A 503 telling the caller to retry needs a machine-readable
		// retry-after or every SDK retry layer falls back to its own short
		// backoff and hammers a control-plane that is, by construction,
		// already unable to answer (PR #903 security review LOW finding).
		status = http.StatusServiceUnavailable
		applyHeaders(w, headers)
	}
	WriteError(w, status, oerr.Error.Type, oerr.Error.Message, oerr.Error.Code)
}
