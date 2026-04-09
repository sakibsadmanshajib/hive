package inference

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// handleEmbeddings handles POST /v1/embeddings.
func handleEmbeddings(o *Orchestrator, w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		writeInvalidBodyError(w)
		return
	}

	var req EmbeddingsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeInvalidBodyError(w)
		return
	}

	if req.Model == "" {
		writeMissingFieldError(w, "model")
		return
	}

	// Validate input is present and non-empty.
	if len(req.Input) == 0 || string(req.Input) == "null" || string(req.Input) == `""` {
		writeMissingFieldError(w, "input")
		return
	}

	// Capability gating: dimensions parameter is only supported on certain models.
	// Models containing "embedding-3" (e.g. text-embedding-3-small, text-embedding-3-large)
	// support custom dimensions. Others do not.
	if req.Dimensions != nil {
		if !supportsDimensions(req.Model) {
			writeUnsupportedParamError(w, "dimensions", req.Model)
			return
		}
	}

	needFlags := NeedFlags{
		NeedEmbeddings: true,
	}

	// Embeddings are cheaper than completions; use 1000 credits as default reservation.
	const estimatedCredits int64 = 1000

	o.executeSync(r.Context(), w, r, EndpointEmbeddings, body, req.Model, needFlags, estimatedCredits,
		o.litellm.Embeddings, normalizeEmbeddings)
}

// supportsDimensions returns true if the model alias supports custom dimensions.
// This is a pragmatic heuristic for Phase 6; a future phase can add a proper
// SupportsDimensions capability flag to the routing types.
func supportsDimensions(model string) bool {
	lower := strings.ToLower(model)
	return strings.Contains(lower, "embedding-3") ||
		strings.Contains(lower, "text-embedding-3")
}

// normalizeEmbeddings normalizes a LiteLLM embeddings response.
func normalizeEmbeddings(respBody []byte, aliasID string) ([]byte, *UsageResponse, error) {
	var resp EmbeddingsResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, nil, err
	}

	// Overwrite model with the Hive alias.
	resp.Model = aliasID

	// Ensure top-level object is "list".
	resp.Object = "list"

	// Ensure each embedding item has the correct object type.
	for i := range resp.Data {
		resp.Data[i].Object = "embedding"
	}

	normalized, err := json.Marshal(resp)
	if err != nil {
		return nil, nil, err
	}

	// Convert EmbeddingsUsage to UsageResponse for the orchestrator.
	var usage *UsageResponse
	if resp.Usage != nil {
		usage = &UsageResponse{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: 0,
			TotalTokens:      resp.Usage.TotalTokens,
		}
	}

	return normalized, usage, nil
}
