package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// ImageGeneration dispatches a JSON body to /images/generations.
func (c *LiteLLMClient) ImageGeneration(ctx context.Context, litellmModel string, body []byte) (*http.Response, error) {
	return c.dispatch(ctx, "/images/generations", litellmModel, body)
}

// ImageEditRaw forwards a pre-built multipart request to /images/edits.
// The caller is responsible for setting the correct Content-Type on the body.
func (c *LiteLLMClient) ImageEditRaw(ctx context.Context, body io.Reader, contentType string) (*http.Response, error) {
	url := c.baseURL + "/images/edits"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("litellm: build image edit request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+c.masterKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("litellm: image edit request failed: %w", err)
	}
	return resp, nil
}

// Speech dispatches a JSON body to /audio/speech and returns the raw binary response.
func (c *LiteLLMClient) Speech(ctx context.Context, litellmModel string, body []byte) (*http.Response, error) {
	return c.dispatch(ctx, "/audio/speech", litellmModel, body)
}

// TranscriptionRaw forwards a pre-built multipart request to /audio/transcriptions.
func (c *LiteLLMClient) TranscriptionRaw(ctx context.Context, body io.Reader, contentType string) (*http.Response, error) {
	url := c.baseURL + "/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("litellm: build transcription request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+c.masterKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("litellm: transcription request failed: %w", err)
	}
	return resp, nil
}

// TranslationRaw forwards a pre-built multipart request to /audio/translations.
func (c *LiteLLMClient) TranslationRaw(ctx context.Context, body io.Reader, contentType string) (*http.Response, error) {
	url := c.baseURL + "/audio/translations"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("litellm: build translation request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+c.masterKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("litellm: translation request failed: %w", err)
	}
	return resp, nil
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
