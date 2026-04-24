package inference

import (
	"encoding/json"
	"net/http"
)

// validateReasoningCapability checks whether the reasoning_effort parameter is allowed
// for the selected route. If reasoning is requested but not supported, it writes an
// OpenAI-style 400 error and returns false. If everything is fine, it returns true.
func validateReasoningCapability(w http.ResponseWriter, model string, reasoningEffort *string, routeSupportsReasoning bool) bool {
	if reasoningEffort == nil {
		return true
	}
	if routeSupportsReasoning {
		return true
	}
	writeUnsupportedParamError(w, "reasoning_effort", model)
	return false
}

// validateResponsesReasoningCapability checks whether the reasoning field is allowed
// for the selected route in the Responses API. If reasoning is requested but not
// supported, it writes an OpenAI-style 400 error and returns false.
func validateResponsesReasoningCapability(w http.ResponseWriter, model string, reasoning json.RawMessage, routeSupportsReasoning bool) bool {
	if len(reasoning) == 0 || string(reasoning) == "null" {
		return true
	}
	if routeSupportsReasoning {
		return true
	}
	writeUnsupportedParamError(w, "reasoning", model)
	return false
}

// normalizeReasoningUsage ensures CompletionTokensDetails and PromptTokensDetails
// exist and are zero-initialized to prevent nil pointer issues in response serialization.
func normalizeReasoningUsage(usage *UsageResponse) {
	if usage == nil {
		return
	}
	if usage.CompletionTokensDetails == nil {
		usage.CompletionTokensDetails = &CompletionTokensDetails{}
	}
	if usage.PromptTokensDetails == nil {
		usage.PromptTokensDetails = &PromptTokensDetails{}
	}
}
