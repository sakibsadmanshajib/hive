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

	"github.com/hivegpt/hive/apps/control-plane/internal/batchstore/executor"
)

// LiteLLMInferenceClient is the production InferencePort that the local batch
// executor uses. Per .planning/phases/15-batch-local-executor/DECISIONS.md
// Q1, the dispatcher calls LiteLLM's /v1/chat/completions directly rather
// than crossing the apps/edge-api/internal/inference module boundary. This
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
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, resp.StatusCode, fmt.Errorf("upstream status %d", resp.StatusCode)
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
