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
	// for a model where the reduction is legitimate. The reduction is always
	// applied client-side; see embedReq for why the `dimensions` request
	// parameter is not sent. 0 means require the native width exactly.
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
//	embedmodel.Resolve and applied client-side to the native-width vector the
//	backend returns.
//
// apiKey is sent as a Bearer token when non-empty (LiteLLM's LITELLM_MASTER_KEY);
// leave empty for backends that require no auth.
func NewHTTPEmbedder(baseURL, model string, reduceTo int, apiKey string) *HTTPEmbedder {
	return &HTTPEmbedder{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		// 60s, not 30s: LiteLLM's own request_timeout is 45s
		// (deploy/litellm/config.yaml), so a 30s client budget gave up before
		// the upstream it is waiting on had. A single Qwen3 8B embedding
		// through OpenRouter measured 35 to 40 seconds on the demo stack, so
		// every query-side embed timed out and RAG search and chat answered
		// 503 while ingestion of the same corpus succeeded.
		client:   &http.Client{Timeout: 60 * time.Second},
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

// embedReq deliberately carries no `dimensions` field. LiteLLM's generic
// `openai/` adapter, which is how every OpenRouter embedding route is declared
// (deploy/litellm/config.yaml), rejects `dimensions` outright for any model
// whose name does not contain "text-embedding-3" and raises
// UnsupportedParamsError with HTTP 400. That check ignores `drop_params`, so no
// LiteLLM setting can make the parameter safe to send, and the whole embedding
// route fails rather than degrading. Requesting the narrower width from the
// endpoint bought nothing anyway: reduceEmbedding performs the identical MRL
// truncate-and-renormalize on the native-width vector.
type embedReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embedVector is one element of an embeddings response. Named rather than
// anonymous so a test can build one without restating the shape, which is
// what makes adding a field here a one-line change instead of five.
type embedVector struct {
	// Index is the position of the input this vector belongs to. The OpenAI
	// embeddings contract does not promise response order, and a batch that
	// came back reordered would rank every chunk against the wrong text, so
	// the batch path below places by it rather than trusting arrival order.
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type embedResp struct {
	Data []embedVector `json:"data"`
}

// Embed embeds a single text string and returns an EmbeddingDimension-wide vector.
// Errors are provider-blind: no backend URL, model name, or upstream message.
func (e *HTTPEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vectors, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

// EmbedBatch embeds every text in ONE request and returns the vectors in the
// order the texts were given.
//
// One request, not one per text. The embeddings API takes an input array and
// always has; sending them one at a time is what produced issue #1609, where
// a single web search fetched five pages and made roughly two hundred
// embedding calls, each taking its own credit hold, until accounting refused
// them and every source was dropped. web_fetch uses this for the whole of a
// page in one call plus one for the query, which is two calls regardless of
// page size.
//
// Single-input Embed is this function with a slice of one, so both paths
// share the request shape, the MRL reduction, the dimension check and the
// provider-blind error contract rather than having two of each.
func (e *HTTPEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("rag.embed: no inputs")
	}
	body, err := json.Marshal(embedReq{Model: e.model, Input: texts})
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

	// The response carries one vector per input, so the ceiling scales with
	// the batch rather than being a fixed figure a legitimate batch can
	// exceed. Roughly 20 bytes per dimension of JSON float, plus headroom.
	// A fixed 4 MiB would have refused a 256-chunk page at 1024 dimensions,
	// which is exactly what web_fetch sends.
	maxResponseBytes := int64(len(texts))*int64(EmbeddingDimension)*20 + 4*1024*1024
	var result embedResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&result); err != nil {
		return nil, fmt.Errorf("rag: embedding service unavailable")
	}

	if len(result.Data) != len(texts) {
		// A short answer is a failure, never a partial success: ranking N
		// chunks against N-1 vectors would silently mis-attribute every one
		// after the gap.
		return nil, fmt.Errorf("rag: embedding service returned %d vectors for %d inputs", len(result.Data), len(texts))
	}

	// The API does not promise response order, so the index field is used
	// when it is a complete permutation of the inputs. Some OpenAI-compatible
	// backends omit it entirely, which arrives as all-zero; that is not a
	// permutation, and arrival order is used instead, which is what the
	// single-input path always relied on.
	byIndex := true
	seen := make([]bool, len(texts))
	for _, item := range result.Data {
		if item.Index < 0 || item.Index >= len(texts) || seen[item.Index] {
			byIndex = false
			break
		}
		seen[item.Index] = true
	}

	out := make([][]float32, len(texts))
	for i, item := range result.Data {
		position := i
		if byIndex {
			position = item.Index
		}
		vec := item.Embedding
		if e.reduceTo > 0 {
			// Legitimate MRL slice; a no-op for a backend that already serves
			// the model at reduceTo width.
			vec = reduceEmbedding(vec, e.reduceTo)
		}
		if len(vec) != EmbeddingDimension {
			return nil, fmt.Errorf("rag: unexpected embedding dimension %d", len(vec))
		}
		out[position] = vec
	}
	return out, nil
}
