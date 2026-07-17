package rag

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestReduceEmbedding verifies the MRL truncate-and-renormalize helper:
// output has the target length and unit L2 norm.
func TestReduceEmbedding(t *testing.T) {
	vec := make([]float32, 4096)
	for i := range vec {
		vec[i] = float32(i%7) - 3 // arbitrary nonzero values
	}

	out := reduceEmbedding(vec, 1024)
	if len(out) != 1024 {
		t.Fatalf("len = %d, want 1024", len(out))
	}

	var sumSq float64
	for _, v := range out {
		sumSq += float64(v) * float64(v)
	}
	norm := math.Sqrt(sumSq)
	if math.Abs(norm-1.0) > 1e-4 {
		t.Errorf("L2 norm = %f, want ~1.0", norm)
	}
}

// TestReduceEmbeddingNoop covers target<=0 and target>=len as no-ops.
func TestReduceEmbeddingNoop(t *testing.T) {
	vec := []float32{1, 2, 3}
	if out := reduceEmbedding(vec, 0); len(out) != 3 {
		t.Errorf("target=0: len = %d, want 3 (noop)", len(out))
	}
	if out := reduceEmbedding(vec, 10); len(out) != 3 {
		t.Errorf("target>len: len = %d, want 3 (noop)", len(out))
	}
}

// TestHTTPEmbedClientTruncates drives a fake 4096-dim backend through
// HTTPEmbedClient with truncateTo=1024 and expects a 1024-dim result per item.
func TestHTTPEmbedClientTruncates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		vec := make([]float32, 4096)
		for i := range vec {
			vec[i] = 0.01
		}
		data := make([]struct {
			Embedding []float32 `json:"embedding"`
		}, len(req.Input))
		for i := range data {
			data[i].Embedding = vec
		}
		_ = json.NewEncoder(w).Encode(embedResponse{Data: data})
	}))
	defer srv.Close()

	c := NewHTTPEmbedClient(srv.URL, "route-openrouter-embedding-fallback", 1024, "")
	vecs, err := c.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("len(vecs) = %d, want 2", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != EmbeddingDimension {
			t.Errorf("item %d: len = %d, want %d", i, len(v), EmbeddingDimension)
		}
	}
}

// TestHTTPEmbedClientStrictRejectByDefault confirms truncateTo=0 (unset) still
// rejects a non-EmbeddingDimension response instead of silently truncating.
func TestHTTPEmbedClientStrictRejectByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vec := make([]float32, 4096)
		_ = json.NewEncoder(w).Encode(embedResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{{Embedding: vec}},
		})
	}))
	defer srv.Close()

	c := NewHTTPEmbedClient(srv.URL, "bge-m3", 0, "")
	if _, err := c.Embed(context.Background(), []string{"hello"}); err == nil {
		t.Fatal("expected dimension-mismatch error, got nil")
	}
}

// TestHTTPEmbedClientSendsAuthHeaderWhenKeySet verifies the LiteLLM master key
// is sent as a Bearer token so /v1/embeddings does not 401.
func TestHTTPEmbedClientSendsAuthHeaderWhenKeySet(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		vec := make([]float32, EmbeddingDimension)
		_ = json.NewEncoder(w).Encode(embedResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{{Embedding: vec}},
		})
	}))
	defer srv.Close()

	c := NewHTTPEmbedClient(srv.URL, "bge-m3", 0, "sk-test-key")
	if _, err := c.Embed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if gotAuth != "Bearer sk-test-key" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer sk-test-key")
	}
}

// TestHTTPEmbedClientOmitsAuthHeaderWhenKeyEmpty verifies a local backend
// (e.g. Ollama) that needs no auth does not receive a bogus header.
func TestHTTPEmbedClientOmitsAuthHeaderWhenKeyEmpty(t *testing.T) {
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization") != ""
		vec := make([]float32, EmbeddingDimension)
		_ = json.NewEncoder(w).Encode(embedResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{{Embedding: vec}},
		})
	}))
	defer srv.Close()

	c := NewHTTPEmbedClient(srv.URL, "bge-m3", 0, "")
	if _, err := c.Embed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if sawAuth {
		t.Error("expected no Authorization header when apiKey is empty")
	}
}
