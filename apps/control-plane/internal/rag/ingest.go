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
	// truncateTo reduces a wider MRL-trained embedding (e.g. 4096-dim) to this
	// many leading dimensions, then L2-renormalizes. 0 disables reduction: a
	// non-EmbeddingDimension vector is rejected outright (see EMBEDDING_TRUNCATE_TO).
	truncateTo int
}

// NewHTTPEmbedClient constructs the production embed client.
// baseURL example: "http://localhost:11434/v1" (Ollama) or "http://litellm:4000".
// model must be the alias returning EmbeddingDimension (or truncateTo) vectors, e.g. "bge-m3".
// truncateTo: 0 requires the backend already return EmbeddingDimension;
// otherwise the target width for MRL truncation (must equal EmbeddingDimension).
func NewHTTPEmbedClient(baseURL, model string, truncateTo int) *HTTPEmbedClient {
	return &HTTPEmbedClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		model:      model,
		client:     &http.Client{Timeout: 30 * time.Second},
		truncateTo: truncateTo,
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
	body, err := json.Marshal(embedRequest{Model: c.model, Input: inputs})
	if err != nil {
		return nil, fmt.Errorf("rag.embed: marshal: %w", err)
	}

	url := c.baseURL + "/v1/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("rag.embed: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

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
		if c.truncateTo > 0 {
			emb = reduceEmbedding(emb, c.truncateTo)
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

	// Embed in batches.
	embeddings := make([][]float32, 0, len(chunks))
	for start := 0; start < len(chunks); start += ing.batchSize {
		end := start + ing.batchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		batch := chunks[start:end]
		texts := make([]string, len(batch))
		for i, c := range batch {
			texts[i] = c.Content
		}
		vecs, err := ing.embed.Embed(ctx, texts)
		if err != nil {
			_ = ing.repo.UpdateDocumentStatus(ctx, tid, did, StatusError, "embedding service unavailable")
			return fmt.Errorf("rag.ingest: embed batch: embedding service unavailable")
		}
		embeddings = append(embeddings, vecs...)
	}

	if err := ing.repo.InsertChunks(ctx, tid, did, chunks, embeddings); err != nil {
		_ = ing.repo.UpdateDocumentStatus(ctx, tid, did, StatusError, "chunk storage failed")
		return fmt.Errorf("rag.ingest: store chunks: %w", err)
	}

	return ing.repo.SetEmbeddedProvenance(ctx, tid, did, ing.embedModel, EmbeddingDimension)
}
