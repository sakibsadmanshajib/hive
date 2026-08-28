package batchstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/batchstore/executor"
)

// LiteLLMInferenceClient is the production InferencePort that the local batch
// executor uses. Per the phase 15 local-executor decision record (see git
// history), the dispatcher calls LiteLLM's /v1/chat/completions directly
// rather than crossing the apps/edge-api/internal/inference module boundary. This
// reuses LiteLLM's provider routing, retry, and capability path — the same
// surface edge-api exposes — while keeping control-plane's go.mod free of
// edge-api/internal imports.
//
// Route resolution happens once per batch in pgxBatchStore.LoadBatch (which
// uses the same NeedBatch=true criteria the submitter applied). The
// resolved LiteLLM model name flows through BatchSnapshot.LiteLLMModel →
// InputLine.LiteLLMModel and is passed verbatim as the model argument
// here — no per-line route lookup, no risk of diverging from the
// submitter's batch-time selection.
// maxLocalInferenceResponseBytes caps a single batch line's completion
// response.
const maxLocalInferenceResponseBytes = 4 * 1024 * 1024

type LiteLLMInferenceClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewLiteLLMInferenceClient constructs the production inference port.
func NewLiteLLMInferenceClient(baseURL, apiKey string) *LiteLLMInferenceClient {
	return &LiteLLMInferenceClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 90 * time.Second},
	}
}

// ChatCompletion calls POST {baseURL}/v1/chat/completions. The model
// argument must already be the LiteLLM-routed model name (resolved once
// per batch by pgxBatchStore.LoadBatch). The body's top-level model field
// is rewritten to that name; all other fields are preserved verbatim.
// Returns the upstream response body, the OpenAI usage object decoded
// from the response, the HTTP status code, and an error.
func (c *LiteLLMInferenceClient) ChatCompletion(ctx context.Context, model string, body json.RawMessage) (json.RawMessage, *executor.Usage, int, error) {
	if strings.TrimSpace(model) == "" {
		return nil, nil, 0, fmt.Errorf("local inference: model is required")
	}
	rewritten, err := rewriteModel(body, model)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("rewrite model: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(rewritten))
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()
	// Read maxLocalInferenceResponseBytes+1, not the cap itself, so a
	// response that actually exceeds the cap is DETECTED rather than
	// silently truncated (issue #1255 finding #2): a plain
	// io.LimitReader(resp.Body, N) truncates without signaling it
	// happened, so a batch line whose completion exceeds this cap would
	// otherwise write a truncated, likely-invalid response into the
	// customer's batch output file, marked as a success, with nothing
	// recording that truncation occurred. Same read-error handling and
	// max+1 detection pattern as apps/edge-api/internal/auth/owui_unwrap.go
	// and apps/edge-api/internal/rag/handler.go's readBodyCapped.
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxLocalInferenceResponseBytes+1))
	if readErr != nil {
		return nil, nil, 0, fmt.Errorf("read upstream response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, resp.StatusCode, fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	if len(respBody) > maxLocalInferenceResponseBytes {
		// status is intentionally 0, not resp.StatusCode: a truncated
		// response is not an upstream HTTP failure, and 0 is the existing
		// "no usable status" convention the dispatcher's retry loop and
		// codeForStatus already handle (the same shape a read/timeout
		// error already produces above), rather than inventing a second
		// failure convention. Wrapping executor.ErrTruncatedUpstreamResponse
		// lets Dispatch recognize this specific, deterministic failure and
		// skip retrying it (PR #1253 review finding), unlike the read
		// error and status-code failures above and below, which stay
		// plain errors and go through the normal retry path.
		return nil, nil, 0, fmt.Errorf("%w: exceeded %d byte limit", executor.ErrTruncatedUpstreamResponse, maxLocalInferenceResponseBytes)
	}

	usage := decodeUsage(respBody)
	return respBody, usage, resp.StatusCode, nil
}

// rewriteModel replaces the top-level "model" field in body with the routed
// LiteLLM model name. Other fields are preserved verbatim. Same approach as
// rewriteBatchJSONL in submitter.go.
func rewriteModel(body json.RawMessage, litellmModel string) (json.RawMessage, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	payload["model"] = litellmModel
	return json.Marshal(payload)
}

// decodeUsage extracts the OpenAI usage object from a chat-completion response.
func decodeUsage(body []byte) *executor.Usage {
	var probe struct {
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &probe); err != nil || probe.Usage == nil {
		return nil
	}
	return &executor.Usage{
		PromptTokens:     probe.Usage.PromptTokens,
		CompletionTokens: probe.Usage.CompletionTokens,
		TotalTokens:      probe.Usage.TotalTokens,
	}
}
