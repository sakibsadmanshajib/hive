package inference

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// handleChatCompletions handles POST /v1/chat/completions.
func handleChatCompletions(o *Orchestrator, w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		writeInvalidBodyError(w)
		return
	}

	var req ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidBodyError(w)
		return
	}

	if req.Model == "" {
		writeMissingFieldError(w, "model")
		return
	}
	if len(req.Messages) == 0 {
		writeMissingFieldError(w, "messages")
		return
	}

	// Reject requests that carry tool-calling or structured-output fields that
	// are not yet supported end-to-end (issue #118). Silently forwarding them
	// causes the provider to ignore the fields and return a plain content
	// response, which misleads SDK consumers into thinking the model did not
	// use the tool. Return an explicit 400 until provider-routing support for
	// these parameters is wired in a future phase.
	if err := rejectUnsupportedChatParams(w, &req); err != nil {
		return
	}

	needFlags := NeedFlags{
		NeedChatCompletions: true,
		NeedStreaming:        req.Stream,
		NeedReasoning:        req.ReasoningEffort != nil,
	}

	if req.Stream {
		includeUsage := req.StreamOptions != nil && req.StreamOptions.IncludeUsage
		o.executeStreaming(r.Context(), w, r, EndpointChatCompletions, body, req.Model, req.Model, needFlags, 10000, includeUsage, req.ReasoningEffort, o.litellm.ChatCompletion)
		return
	}

	o.executeSync(r.Context(), w, r, EndpointChatCompletions, body, req.Model, needFlags, 10000,
		o.litellm.ChatCompletion, normalizeChatCompletion)
}

// normalizeChatCompletion normalizes a LiteLLM chat completion response.
func normalizeChatCompletion(respBody []byte, aliasID string) ([]byte, *UsageResponse, error) {
	var resp ChatCompletionResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, nil, err
	}

	resp.Model = aliasID
	resp.Object = "chat.completion"

	clampZeroCompletionUsage(resp.Usage, chatChoiceTexts(resp.Choices), resp.ID, aliasID, EndpointChatCompletions)

	normalized, err := json.Marshal(resp)
	if err != nil {
		return nil, nil, err
	}

	return normalized, resp.Usage, nil
}

// rejectUnsupportedChatParams checks for fields that are parsed and present in
// ChatCompletionRequest but not yet supported end-to-end. It writes a 400
// response and returns a non-nil sentinel error when any unsupported parameter
// is present, so the caller can return immediately.
func rejectUnsupportedChatParams(w http.ResponseWriter, req *ChatCompletionRequest) error {
	var param string
	switch {
	case len(req.Tools) > 0:
		param = "tools"
	case len(req.ToolChoice) > 0:
		param = "tool_choice"
	case len(req.ResponseFormat) > 0:
		param = "response_format"
	case len(req.Functions) > 0:
		param = "functions"
	case len(req.FunctionCall) > 0:
		param = "function_call"
	}

	if param == "" && req.ParallelToolCalls != nil {
		param = "parallel_tool_calls"
	}

	if param == "" {
		return nil
	}

	code := "unsupported_parameter"
	writeError(w, http.StatusBadRequest, "invalid_request_error",
		"The '"+param+"' parameter is not yet supported. Tool calling and structured output will be available in a future release.",
		&code,
	)
	return errors.New("unsupported parameter: " + param)
}

// writeError is a local helper to write OpenAI-style errors.
func writeError(w http.ResponseWriter, status int, errType, message string, code *string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    errType,
			"param":   nil,
			"code":    code,
		},
	})
}
