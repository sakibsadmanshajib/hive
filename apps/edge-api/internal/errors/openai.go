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
type OpenAIErrorBody struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
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

// WriteRateLimitError writes a 429 OpenAI-style error with retry metadata headers.
func WriteRateLimitError(w http.ResponseWriter, message string, code *string, headers map[string]string) {
	for key, value := range headers {
		if strings.TrimSpace(value) == "" {
			continue
		}
		w.Header().Set(key, value)
	}
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
	if oerr.Error.Code != nil && *oerr.Error.Code == "rate_limit_exceeded" {
		WriteRateLimitError(w, oerr.Error.Message, oerr.Error.Code, headers)
		return
	}
	status := http.StatusUnauthorized
	switch {
	case oerr.Error.Type == "insufficient_quota":
		status = http.StatusTooManyRequests
	case oerr.Error.Code != nil && *oerr.Error.Code == "model_not_found":
		status = http.StatusNotFound
	}
	WriteError(w, status, oerr.Error.Type, oerr.Error.Message, oerr.Error.Code)
}
