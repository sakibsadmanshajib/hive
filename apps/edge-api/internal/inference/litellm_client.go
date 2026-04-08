package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// LiteLLMClient dispatches inference requests to the LiteLLM proxy.
type LiteLLMClient struct {
	baseURL    string
	masterKey  string
	httpClient *http.Client
}

// NewLiteLLMClient creates a new LiteLLMClient.
func NewLiteLLMClient(baseURL, masterKey string) *LiteLLMClient {
	return &LiteLLMClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		masterKey:  masterKey,
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// ChatCompletion dispatches a chat completion request to LiteLLM.
// The caller owns closing the returned response body.
func (c *LiteLLMClient) ChatCompletion(ctx context.Context, litellmModel string, body []byte) (*http.Response, error) {
	return c.dispatch(ctx, "/chat/completions", litellmModel, body)
}

// Completion dispatches a legacy completion request to LiteLLM.
func (c *LiteLLMClient) Completion(ctx context.Context, litellmModel string, body []byte) (*http.Response, error) {
	return c.dispatch(ctx, "/completions", litellmModel, body)
}

// Embeddings dispatches an embeddings request to LiteLLM.
func (c *LiteLLMClient) Embeddings(ctx context.Context, litellmModel string, body []byte) (*http.Response, error) {
	return c.dispatch(ctx, "/embeddings", litellmModel, body)
}

func (c *LiteLLMClient) dispatch(ctx context.Context, path, litellmModel string, body []byte) (*http.Response, error) {
	rewritten := rewriteModel(body, litellmModel)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+path, bytes.NewReader(rewritten))
	if err != nil {
		return nil, fmt.Errorf("litellm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.masterKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("litellm: request failed: %w", err)
	}

	return resp, nil
}

// rewriteModel replaces the "model" field in the JSON body with the LiteLLM model name.
func rewriteModel(body []byte, newModel string) []byte {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(body, &parsed); err != nil {
		return body
	}

	modelJSON, err := json.Marshal(newModel)
	if err != nil {
		return body
	}
	parsed["model"] = modelJSON

	result, err := json.Marshal(parsed)
	if err != nil {
		return body
	}

	return result
}
