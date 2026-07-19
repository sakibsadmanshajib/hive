package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// Embedder produces EmbeddingDimension-wide vectors for text queries.
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
	// reduceTo is the MRL reduction target, derived from embedmodel.Resolve:
	// it is EmbeddingDimension when the configured model is MRL-trained and its
	// chosen dim is below its native width, else 0. It is NOT an independent
	// operator knob (the old EMBEDDING_TRUNCATE_TO): a non-MRL model at a
	// non-native dim is rejected at config time, so reduceTo is only ever set
	// for a model where the reduction is legitimate. It doubles as the
	// `dimensions` value requested from the endpoint (preferred over client
	// slicing); when the endpoint honors it and returns a dim-wide vector, the
	// client-side reduce is a no-op. 0 means require the native width exactly.
	reduceTo int
	// apiKey authenticates to the backend (LiteLLM requires it; a local
	// bge-m3/Ollama endpoint does not). Empty means send no Authorization header.
	apiKey string
}

// NewHTTPEmbedder constructs the production embedder.
// baseURL:  e.g. "http://ollama:11434/v1" or "http://litellm:4000".
// model:    the alias returning EmbeddingDimension vectors, e.g. "bge-m3".
// reduceTo: 0 to require the backend already return EmbeddingDimension;
//
//	otherwise the MRL reduction target (== EmbeddingDimension), derived from
//	embedmodel.Resolve, sent to the endpoint as `dimensions` and applied
//	client-side only if the endpoint ignores it.
//
// apiKey is sent as a Bearer token when non-empty (LiteLLM's LITELLM_MASTER_KEY);
// leave empty for backends that require no auth.
func NewHTTPEmbedder(baseURL, model string, reduceTo int, apiKey string) *HTTPEmbedder {
	return &HTTPEmbedder{
		baseURL:  strings.TrimRight(baseURL, "/"),
		model:    model,
		client:   &http.Client{Timeout: 30 * time.Second},
		reduceTo: reduceTo,
		apiKey:   apiKey,
	}
}

// reduceEmbedding implements Matryoshka Representation Learning (MRL)
// truncation: keep the first `target` dimensions and L2-renormalize so the
// result is a valid unit-ish vector for cosine similarity. Only correct for
// MRL-trained embedding models; never apply to an arbitrary model.
func reduceEmbedding(vec []float32, target int) []float32 {
	if target <= 0 || target >= len(vec) {
		return vec
	}
	out := make([]float32, target)
	copy(out, vec[:target])
	var sumSq float64
	for _, v := range out {
		sumSq += float64(v) * float64(v)
	}
	norm := math.Sqrt(sumSq)
	if norm == 0 {
		return out
	}
	for i, v := range out {
		out[i] = float32(float64(v) / norm)
	}
	return out
}

type embedReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
	// Dimensions requests a native N-wide vector from an MRL-capable endpoint
	// (Qwen3 supports it). Omitted when 0 so non-MRL native-width models are
	// unaffected. Preferred over client-side slicing; the reduceEmbedding
	// fallback covers an endpoint that silently ignores the parameter.
	Dimensions int `json:"dimensions,omitempty"`
}

type embedResp struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed embeds a single text string and returns an EmbeddingDimension-wide vector.
// Errors are provider-blind: no backend URL, model name, or upstream message.
func (e *HTTPEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(embedReq{Model: e.model, Input: []string{text}, Dimensions: e.reduceTo})
	if err != nil {
		return nil, fmt.Errorf("rag.embed: marshal: %w", err)
	}

	url := e.baseURL + "/v1/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rag.embed: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

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
	if e.reduceTo > 0 {
		// No-op when the endpoint already honored `dimensions` and returned a
		// reduceTo-wide vector; a legitimate MRL slice otherwise.
		vec = reduceEmbedding(vec, e.reduceTo)
	}
	if len(vec) != EmbeddingDimension {
		return nil, fmt.Errorf("rag: unexpected embedding dimension %d", len(vec))
	}
	return vec, nil
}
