package inference

import (
	"encoding/json"
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

	if req.Stream {
		code := "not_implemented"
		writeError(w, http.StatusNotImplemented, "api_error", "Streaming is not yet available.", &code)
		return
	}

	needFlags := NeedFlags{
		NeedChatCompletions: true,
		NeedStreaming:        req.Stream,
		NeedReasoning:        req.ReasoningEffort != nil,
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

	normalized, err := json.Marshal(resp)
	if err != nil {
		return nil, nil, err
	}

	return normalized, resp.Usage, nil
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
