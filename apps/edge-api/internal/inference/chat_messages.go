package inference

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// chatRoles is the set of message roles the OpenAI chat surface defines.
// function is the deprecated spelling and is still accepted, because a caller
// on an older SDK is not making a mistake this gateway should invent.
var chatRoles = []string{"system", "developer", "user", "assistant", "tool", "function"}

// validateChatMessages refuses a malformed messages array at the gateway,
// before anything is dispatched (#1348).
//
// It was previously forwarded verbatim, so an unknown role became an upstream
// 400 that arrived at the caller as "hive-small is not available." with code
// upstream_error: a sentence about model availability for a fault entirely
// inside the caller payload, and an upstream code for a request no upstream
// needed to see. Every SDK in the support matrix understands
// invalid_request_error with a param, which is what the n refusal in
// writeUnsupportedChoiceCountError already returns.
//
// It reports whether the caller should continue, and writes the refusal
// itself when not.
//
// ponytail: role and arity only. Full per-role schema validation (tool
// messages needing tool_call_id, content shapes per role) is a much larger
// contract to own and to keep in step with upstreams that each accept a
// slightly different dialect; the two checks here are the ones that are
// unambiguous, cheap, and were actually measured failing.
func validateChatMessages(w http.ResponseWriter, raw json.RawMessage) bool {
	var messages []struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(raw, &messages); err != nil {
		writeInvalidMessagesError(w, "messages", "invalid_type",
			"The messages field must be an array of message objects.")
		return false
	}
	if len(messages) == 0 {
		writeInvalidMessagesError(w, "messages", "empty_array",
			"The messages field must contain at least one message.")
		return false
	}
	for i, message := range messages {
		if validChatRole(message.Role) {
			continue
		}
		param := fmt.Sprintf("messages[%d].role", i)
		writeInvalidMessagesError(w, param, "invalid_value",
			fmt.Sprintf("Invalid value for %s. Supported values are: %s.", param, strings.Join(chatRoles, ", ")))
		return false
	}
	return true
}

// validChatRole reports whether role is one the chat surface accepts.
func validChatRole(role string) bool {
	for _, known := range chatRoles {
		if role == known {
			return true
		}
	}
	return false
}

// writeInvalidMessagesError writes the OpenAI-shaped refusal for a malformed
// messages array: invalid_request_error, the offending param, and a code an
// SDK can branch on. Provider-blind by construction, since nothing here comes
// from an upstream or names one.
func writeInvalidMessagesError(w http.ResponseWriter, param, code, message string) {
	apierrors.WriteErrorWithParam(w, http.StatusBadRequest, "invalid_request_error", message, &code, param)
}
