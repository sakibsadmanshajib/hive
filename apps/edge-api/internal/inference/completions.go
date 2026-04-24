package inference

import (
	"encoding/json"
	"io"
	"net/http"
)

// handleCompletions handles POST /v1/completions.
func handleCompletions(o *Orchestrator, w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		writeInvalidBodyError(w)
		return
	}

	var req CompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidBodyError(w)
		return
	}

	if req.Model == "" {
		writeMissingFieldError(w, "model")
		return
	}
	if len(req.Prompt) == 0 {
		writeMissingFieldError(w, "prompt")
		return
	}

	// LiteLLM routes legacy completions through chat/completions-capable routes.
	needFlags := NeedFlags{
		NeedChatCompletions: true,
		NeedStreaming:        req.Stream,
	}

	if req.Stream {
		includeUsage := req.StreamOptions != nil && req.StreamOptions.IncludeUsage
		o.executeStreaming(r.Context(), w, r, EndpointCompletions, body, req.Model, req.Model, needFlags, 10000, includeUsage, nil, o.litellm.Completion)
		return
	}

	o.executeSync(r.Context(), w, r, EndpointCompletions, body, req.Model, needFlags, 10000,
		o.litellm.Completion, normalizeCompletion)
}

// normalizeCompletion normalizes a LiteLLM legacy completion response.
func normalizeCompletion(respBody []byte, aliasID string) ([]byte, *UsageResponse, error) {
	var resp CompletionResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, nil, err
	}

	resp.Model = aliasID
	resp.Object = "text_completion"

	clampZeroCompletionUsage(resp.Usage, completionChoiceTexts(resp.Choices), resp.ID, aliasID, EndpointCompletions)

	normalized, err := json.Marshal(resp)
	if err != nil {
		return nil, nil, err
	}

	return normalized, resp.Usage, nil
}
