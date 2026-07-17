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

	// Leading dims are preserved up to the rescale factor.
	scale := out[0] / vec[0]
	for i := 1; i < len(out); i++ {
		if vec[i] == 0 {
			continue
		}
		got := out[i] / vec[i]
		if math.Abs(float64(got-scale)) > 1e-3 {
			t.Errorf("dim %d not proportionally scaled: got %f, want %f", i, got, scale)
		}
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

// TestHTTPEmbedderTruncates drives a fake 4096-dim backend through
// HTTPEmbedder with truncateTo=1024 and expects a 1024-dim result.
func TestHTTPEmbedderTruncates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vec := make([]float32, 4096)
		for i := range vec {
			vec[i] = 0.01
		}
		_ = json.NewEncoder(w).Encode(embedResp{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{{Embedding: vec}},
		})
	}))
	defer srv.Close()

	e := NewHTTPEmbedder(srv.URL, "route-openrouter-embedding-fallback", 1024)
	vec, err := e.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(vec) != EmbeddingDimension {
		t.Errorf("len = %d, want %d", len(vec), EmbeddingDimension)
	}
}

// TestHTTPEmbedderStrictRejectByDefault confirms truncateTo=0 (unset) still
// rejects a non-EmbeddingDimension response instead of silently truncating.
func TestHTTPEmbedderStrictRejectByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vec := make([]float32, 4096)
		_ = json.NewEncoder(w).Encode(embedResp{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{{Embedding: vec}},
		})
	}))
	defer srv.Close()

	e := NewHTTPEmbedder(srv.URL, "bge-m3", 0)
	if _, err := e.Embed(context.Background(), "hello"); err == nil {
		t.Fatal("expected dimension-mismatch error, got nil")
	}
}
