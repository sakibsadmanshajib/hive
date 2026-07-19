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

// EmbedClient calls the local embedding service (bge-m3 via Ollama/LiteLLM).
// It is kept as an interface so tests can inject a fake without unsafe casts.
type EmbedClient interface {
	// Embed returns an EmbeddingDimension-wide vector for each input string.
	// Returns an error when the backend is unavailable (provider-blind to callers).
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}

// HTTPEmbedClient is the production EmbedClient. It posts to the OpenAI-compatible
// embeddings endpoint on the configured local service (EMBEDDING_BASE_URL).
type HTTPEmbedClient struct {
	baseURL string
	model   string
	client  *http.Client
	// reduceTo is the MRL reduction target, derived from embedmodel.Resolve:
	// EmbeddingDimension when the configured model is MRL-trained and its
	// chosen dim is below its native width, else 0. Not an independent operator
	// knob (the old EMBEDDING_TRUNCATE_TO): a non-MRL model at a non-native dim
	// is rejected at config time, so reduceTo is only set where the reduction
	// is legitimate. It doubles as the `dimensions` value requested from the
	// endpoint; the client-side reduce is a no-op when the endpoint honors it.
	// 0 means require the native width exactly.
	reduceTo int
	// apiKey authenticates to the backend (LiteLLM requires it; a local
	// bge-m3/Ollama endpoint does not). Empty means send no Authorization header.
	apiKey string
}

// NewHTTPEmbedClient constructs the production embed client.
// baseURL example: "http://localhost:11434/v1" (Ollama) or "http://litellm:4000".
// model must be the alias returning EmbeddingDimension vectors, e.g. "bge-m3".
// reduceTo: 0 requires the backend already return EmbeddingDimension; otherwise
// the MRL reduction target (== EmbeddingDimension) derived from
// embedmodel.Resolve, sent as `dimensions` and applied client-side only if the
// endpoint ignores it.
// apiKey is sent as a Bearer token when non-empty (LiteLLM's LITELLM_MASTER_KEY);
// leave empty for backends that require no auth.
func NewHTTPEmbedClient(baseURL, model string, reduceTo int, apiKey string) *HTTPEmbedClient {
	return &HTTPEmbedClient{
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

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
	// Dimensions requests a native N-wide vector from an MRL-capable endpoint
	// (Qwen3 supports it). Omitted when 0; the reduceEmbedding fallback covers
	// an endpoint that ignores it.
	Dimensions int `json:"dimensions,omitempty"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed calls POST /v1/embeddings on the local service.
// Errors are wrapped with a provider-blind message — callers must not
// expose the raw error to customers.
func (c *HTTPEmbedClient) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	body, err := json.Marshal(embedRequest{Model: c.model, Input: inputs, Dimensions: c.reduceTo})
	if err != nil {
		return nil, fmt.Errorf("rag.embed: marshal: %w", err)
	}

	url := c.baseURL + "/v1/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rag.embed: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		// Provider-blind: do not include URL or model name in wrapped error.
		return nil, fmt.Errorf("rag.embed: embedding service unavailable")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rag.embed: embedding service returned status %d", resp.StatusCode)
	}

	var result embedResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16*1024*1024)).Decode(&result); err != nil {
		return nil, fmt.Errorf("rag.embed: decode response: %w", err)
	}

	if len(result.Data) != len(inputs) {
		return nil, fmt.Errorf("rag.embed: expected %d embeddings, got %d", len(inputs), len(result.Data))
	}

	out := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		emb := d.Embedding
		if c.reduceTo > 0 {
			// No-op when the endpoint already returned a reduceTo-wide vector.
			emb = reduceEmbedding(emb, c.reduceTo)
		}
		if len(emb) != EmbeddingDimension {
			return nil, fmt.Errorf("rag.embed: item %d has dimension %d, want %d",
				i, len(emb), EmbeddingDimension)
		}
		out[i] = emb
	}
	return out, nil
}

// Ingester chunks a document, embeds each chunk, and stores results.
type Ingester struct {
	repo  *Repo
	embed EmbedClient
	// batchSize controls how many chunks are embedded in one HTTP call.
	// ponytail: global constant, tune if embed service has payload limits.
	batchSize int
	// embedModel is recorded as rag_documents.embedding_model provenance for
	// every document this Ingester embeds (see SetEmbeddedProvenance).
	embedModel string
}

// NewIngester creates an Ingester. batchSize 0 defaults to 32. embedModel is
// the EMBEDDING_MODEL this Ingester's embed client was constructed with; it
// is stamped onto every document as provenance (paired with the current
// EmbeddingDimension) so a later model swap can find which documents still
// need re-embedding.
func NewIngester(repo *Repo, embed EmbedClient, batchSize int, embedModel string) *Ingester {
	if batchSize <= 0 {
		batchSize = 32
	}
	return &Ingester{repo: repo, embed: embed, batchSize: batchSize, embedModel: embedModel}
}

// Ingest chunks text, embeds all chunks, persists to rag_chunks, and
// updates rag_documents.status to 'embedded'. On any error the document
// status is set to 'error' with a provider-blind message.
func (ing *Ingester) Ingest(ctx context.Context, tenantID, docID interface{ String() string }, text string) error {
	// Avoid importing uuid here; accept fmt.Stringer so the caller passes uuid.UUID.
	tid, err := parseUUID(tenantID.String())
	if err != nil {
		return fmt.Errorf("rag.ingest: invalid tenantID: %w", err)
	}
	did, err := parseUUID(docID.String())
	if err != nil {
		return fmt.Errorf("rag.ingest: invalid docID: %w", err)
	}

	// Mark processing.
	if err := ing.repo.UpdateDocumentStatus(ctx, tid, did, StatusProcessing, ""); err != nil {
		return fmt.Errorf("rag.ingest: set processing: %w", err)
	}

	chunks := ChunkText(text, DefaultChunkTokens, DefaultOverlapTokens)
	if len(chunks) == 0 {
		_ = ing.repo.UpdateDocumentStatus(ctx, tid, did, StatusError, "document produced no text chunks")
		return fmt.Errorf("rag.ingest: document produced no text chunks")
	}

	// Embed in batches (shared with the re-embed worker: fail-closed on any
	// batch error so a partial vector set is never stored).
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}
	embeddings, err := embedInBatches(ctx, ing.embed, texts, ing.batchSize)
	if err != nil {
		_ = ing.repo.UpdateDocumentStatus(ctx, tid, did, StatusError, "embedding service unavailable")
		return fmt.Errorf("rag.ingest: embed batch: embedding service unavailable")
	}

	if err := ing.repo.InsertChunks(ctx, tid, did, chunks, embeddings); err != nil {
		_ = ing.repo.UpdateDocumentStatus(ctx, tid, did, StatusError, "chunk storage failed")
		return fmt.Errorf("rag.ingest: store chunks: %w", err)
	}

	return ing.repo.SetEmbeddedProvenance(ctx, tid, did, ing.embedModel, EmbeddingDimension)
}
