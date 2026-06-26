package rag

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

// Embedder produces 1024-dim vectors for text queries.
// The interface keeps the handler testable without a real HTTP backend.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// HTTPEmbedder calls the local embedding service (OpenAI-compatible endpoint).
// EMBEDDING_BASE_URL points at Ollama or LiteLLM on the enterprise box.
type HTTPEmbedder struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewHTTPEmbedder constructs the production embedder.
// baseURL: e.g. "http://ollama:11434/v1" or "http://litellm:4000".
// model:   the alias that returns EmbeddingDimension vectors, e.g. "bge-m3".
func NewHTTPEmbedder(baseURL, model string) *HTTPEmbedder {
	return &HTTPEmbedder{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

type embedReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResp struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed embeds a single text string and returns a 1024-dim vector.
// Errors are provider-blind: no backend URL, model name, or upstream message.
func (e *HTTPEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(embedReq{Model: e.model, Input: []string{text}})
	if err != nil {
		return nil, fmt.Errorf("rag.embed: marshal: %w", err)
	}

	url := e.baseURL + "/v1/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rag.embed: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		// Provider-blind: omit URL and model.
		return nil, fmt.Errorf("rag: embedding service unavailable")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rag: embedding service unavailable")
	}

	var result embedResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4*1024*1024)).Decode(&result); err != nil {
		return nil, fmt.Errorf("rag: embedding service unavailable")
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("rag: embedding service returned no data")
	}
	vec := result.Data[0].Embedding
	if len(vec) != EmbeddingDimension {
		return nil, fmt.Errorf("rag: unexpected embedding dimension %d", len(vec))
	}
	return vec, nil
}
