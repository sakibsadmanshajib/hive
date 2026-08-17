package anthropic

import (
	"encoding/json"
	"net/http"

	apierr "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// anthropicErrorEnvelope is the wire shape every real Anthropic Messages error
// response uses: a top-level "type":"error" plus a nested error object, e.g.
//
//	{"type":"error","error":{"type":"authentication_error","message":"..."}}
//
// The rest of edge-api uses the OpenAI envelope ({"error":{"message","type",
// "param","code"}}, no top-level "type"), which this surface used
// unconditionally before this file existed. The Anthropic SDK's exception
// CLASS selection keys off HTTP status, not body shape, so that half kept
// working either way; but anthropic._exceptions.APIStatusError reads
// body["error"]["type"] into its own .type attribute, and the OpenAI envelope
// both lacks the wrapping "type":"error" and carries the wrong enum member
// there (e.g. "UNAUTHORIZED", which is not one of Anthropic's documented
// error types). A caller inspecting either field got nothing usable.
type anthropicErrorEnvelope struct {
	Type  string             `json:"type"`
	Error anthropicErrorBody `json:"error"`
}

type anthropicErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	// Code is a Hive extension carrying the finer-grained OpenAI-style error
	// code where the delegated chain supplied one (e.g. "invalid_api_key").
	// The real Anthropic API never sends this field; an Anthropic SDK ignores
	// unknown JSON fields, so surfacing it here is additive, not a compliance
	// risk, and keeps the detail available for anyone inspecting the raw body.
	Code string `json:"code,omitempty"`
}

// anthropicErrorType maps an HTTP status to the closest member of Anthropic's
// documented error-type enum: invalid_request_error, authentication_error,
// permission_error, not_found_error, rate_limit_error, api_error,
// overloaded_error, request_too_large. Status is authoritative here, not
// whatever "type" string a delegated OpenAI-shaped body carried: status is
// what actually drives the real SDK's exception class, so it is the one
// value this surface must never get wrong.
func anthropicErrorType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case 529: // Anthropic's own "overloaded" status; we do not emit it today.
		return "overloaded_error"
	default:
		return "api_error"
	}
}

// writeAnthropicError writes the Anthropic-shaped error envelope. code is the
// optional Hive-extension field (see anthropicErrorBody.Code) and may be "".
func writeAnthropicError(w http.ResponseWriter, status int, message string, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(anthropicErrorEnvelope{
		Type: "error",
		Error: anthropicErrorBody{
			Type:    anthropicErrorType(status),
			Message: message,
			Code:    code,
		},
	})
}

// reshapeToAnthropicError re-emits an already-sanitized OpenAI-shaped error
// body (the delegated chat/inference chain's own refusal, which already ran
// through the provider-blind sanitizer at the upstream boundary) in
// Anthropic's envelope. message and code are extracted best-effort from raw;
// raw may be non-JSON, carry no error object, or be empty, in which case a
// generic message for the status is used instead. This never re-sanitizes:
// it only reshapes an envelope that was already safe to return.
func reshapeToAnthropicError(w http.ResponseWriter, status int, raw []byte) {
	message, code := extractOpenAIErrorFields(raw)
	if message == "" {
		message = genericMessageForStatus(status)
	}
	writeAnthropicError(w, status, message, code)
}

func extractOpenAIErrorFields(raw []byte) (message string, code string) {
	var body apierr.OpenAIError
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", ""
	}
	if body.Error.Code != nil {
		code = *body.Error.Code
	}
	return body.Error.Message, code
}

func genericMessageForStatus(status int) string {
	switch status {
	case http.StatusTooManyRequests:
		return "the request was rate limited."
	case http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusBadGateway:
		return "the request could not be completed."
	default:
		return "the request failed."
	}
}
