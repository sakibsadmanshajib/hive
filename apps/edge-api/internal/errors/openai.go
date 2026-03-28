package errors

import (
	"encoding/json"
	"net/http"
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
