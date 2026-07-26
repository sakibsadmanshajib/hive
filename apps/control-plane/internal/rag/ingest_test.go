package rag

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// fakeEmbedClient returns a fixed 1024-dim vector for every input.
// Structurally valid: slice of the correct length, no unsafe casts.
type fakeEmbedClient struct {
	calls  int
	failOn int // if > 0, return error on this call index (1-based)
	lastIn []string
}

func (f *fakeEmbedClient) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	f.calls++
	f.lastIn = inputs
	if f.failOn > 0 && f.calls >= f.failOn {
		return nil, errors.New("embedding service unavailable")
	}
	out := make([][]float32, len(inputs))
	for i := range inputs {
		v := make([]float32, EmbeddingDimension)
		for j := range v {
			v[j] = float32(i+1) / float32(EmbeddingDimension)
		}
		out[i] = v
	}
	return out, nil
}

// fakeRepo records calls without a real DB. It satisfies ingestRepo, so the
// tests below drive the real Ingester.Ingest rather than a copy of it.
type fakeRepo struct {
	inserted        []Document
	statusUpdates   []string
	chunks          []Chunk
	embeddings      [][]float32
	getErr          error
	provenanceModel string
	provenanceDim   int
}

func (f *fakeRepo) SetEmbeddedProvenance(_ context.Context, _, _ uuid.UUID, model string, dim int) error {
	f.statusUpdates = append(f.statusUpdates, StatusEmbedded)
	f.provenanceModel = model
	f.provenanceDim = dim
	return nil
}

func (f *fakeRepo) InsertDocument(_ context.Context, d Document) (uuid.UUID, error) {
	f.inserted = append(f.inserted, d)
	return uuid.New(), nil
}

func (f *fakeRepo) UpdateDocumentStatus(_ context.Context, _, _ uuid.UUID, status, _ string) error {
	f.statusUpdates = append(f.statusUpdates, status)
	return nil
}

func (f *fakeRepo) InsertChunks(_ context.Context, _, _ uuid.UUID, chunks []Chunk, embs [][]float32) error {
	f.chunks = append(f.chunks, chunks...)
	f.embeddings = append(f.embeddings, embs...)
	return nil
}

// TestIngester_HappyPath verifies chunking + embedding + storage flow.
func TestIngester_HappyPath(t *testing.T) {
	embed := &fakeEmbedClient{}
	repo := &fakeRepo{}

	ing := NewIngester(repo, embed, 0, "route-openrouter-embedding-fallback")

	tenantID := uuid.New()
	docID := uuid.New()
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 60)

	err := ing.Ingest(context.Background(), tenantID, docID, text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if embed.calls == 0 {
		t.Error("embed should have been called at least once")
	}
	if len(repo.chunks) == 0 {
		t.Error("expected chunks to be stored")
	}
	if len(repo.chunks) != len(repo.embeddings) {
		t.Errorf("chunk/embedding count mismatch: %d vs %d", len(repo.chunks), len(repo.embeddings))
	}
	for _, emb := range repo.embeddings {
		if len(emb) != EmbeddingDimension {
			t.Errorf("embedding dimension %d, want %d", len(emb), EmbeddingDimension)
		}
	}

	// Status flow: processing -> embedded.
	if len(repo.statusUpdates) < 2 {
		t.Fatalf("expected at least 2 status updates, got %d: %v", len(repo.statusUpdates), repo.statusUpdates)
	}
	first := repo.statusUpdates[0]
	last := repo.statusUpdates[len(repo.statusUpdates)-1]
	if first != StatusProcessing {
		t.Errorf("first status update = %q, want %q", first, StatusProcessing)
	}
	if last != StatusEmbedded {
		t.Errorf("last status update = %q, want %q", last, StatusEmbedded)
	}
}

// TestIngester_StampsCanonicalProvenance pins the provenance spelling.
// EMBEDDING_MODEL is normally a LiteLLM route alias, while the re-embed worker
// selects and stamps the canonical registry id. Stamping the raw alias made
// every fresh document look stale to the catch-up worker and tripped edge-api's
// consistency guard, which failed RAG search and chat closed for a corpus that
// was embedded under exactly the configured model.
func TestIngester_StampsCanonicalProvenance(t *testing.T) {
	embed := &fakeEmbedClient{}
	repo := &fakeRepo{}
	ing := NewIngester(repo, embed, 0, "route-openrouter-embedding-fallback")

	if err := ing.Ingest(context.Background(), uuid.New(), uuid.New(),
		strings.Repeat("Grounding content. ", 50)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if want := "qwen3-embedding-8b"; repo.provenanceModel != want {
		t.Errorf("provenance model = %q, want canonical %q", repo.provenanceModel, want)
	}
	if repo.provenanceDim != EmbeddingDimension {
		t.Errorf("provenance dim = %d, want %d", repo.provenanceDim, EmbeddingDimension)
	}
}

// TestIngester_EmbedFailure verifies provider-blind error propagation and status=error.
func TestIngester_EmbedFailure(t *testing.T) {
	embed := &fakeEmbedClient{failOn: 1}
	repo := &fakeRepo{}
	ing := NewIngester(repo, embed, 0, "route-openrouter-embedding-fallback")

	err := ing.Ingest(context.Background(), uuid.New(), uuid.New(),
		strings.Repeat("Some content. ", 50))
	if err == nil {
		t.Fatal("expected error from embed failure")
	}
	if strings.Contains(err.Error(), "ollama") || strings.Contains(err.Error(), "litellm") ||
		strings.Contains(err.Error(), "openai") {
		t.Errorf("error leaks provider name: %v", err)
	}

	var sawError bool
	for _, s := range repo.statusUpdates {
		if s == StatusError {
			sawError = true
		}
	}
	if !sawError {
		t.Errorf("expected document status=error after embed failure, got: %v", repo.statusUpdates)
	}
}

// TestIngester_EmptyText verifies that an empty document is rejected cleanly.
func TestIngester_EmptyText(t *testing.T) {
	embed := &fakeEmbedClient{}
	repo := &fakeRepo{}
	ing := NewIngester(repo, embed, 0, "route-openrouter-embedding-fallback")

	err := ing.Ingest(context.Background(), uuid.New(), uuid.New(), "")
	if err == nil {
		t.Fatal("expected error for empty text")
	}
}

// TestIngester_BatchBoundary verifies multi-batch embedding when chunk count > batchSize.
func TestIngester_BatchBoundary(t *testing.T) {
	embed := &fakeEmbedClient{}
	repo := &fakeRepo{}
	// batchSize=1 forces one embed call per chunk; we need >= 3 chunks.
	ing := NewIngester(repo, embed, 1, "route-openrouter-embedding-fallback")

	// Build text long enough to produce multiple chunks at DefaultChunkTokens.
	// DefaultChunkTokens=512 => ~2048 chars per chunk. 20000 chars => ~10 chunks.
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 450)
	err := ing.Ingest(context.Background(), uuid.New(), uuid.New(), text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if embed.calls < 2 {
		t.Errorf("expected >= 2 embed calls for batchSize=1, got %d", embed.calls)
	}
}
