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

// TestHTTPEmbedderReducesWhenEndpointIgnoresDimensions drives a fake backend
// that ignores `dimensions` and returns 4096-dim; the client-side MRL reduce
// fallback must bring it to EmbeddingDimension. It also asserts the client
// asked for the narrower width via `dimensions` (preferred endpoint-native).
func TestHTTPEmbedderReducesWhenEndpointIgnoresDimensions(t *testing.T) {
	var gotDims int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotDims = req.Dimensions
		vec := make([]float32, 4096) // endpoint ignores dimensions, returns native
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

	e := NewHTTPEmbedder(srv.URL, "route-openrouter-embedding-fallback", 1024, "")
	vec, err := e.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if gotDims != 1024 {
		t.Errorf("requested dimensions = %d, want 1024 (endpoint-native preferred)", gotDims)
	}
	if len(vec) != EmbeddingDimension {
		t.Errorf("len = %d, want %d", len(vec), EmbeddingDimension)
	}
}

// TestHTTPEmbedderEndpointHonorsDimensions covers the preferred path: the
// endpoint honors `dimensions` and returns EmbeddingDimension natively, so the
// client-side reduce is a no-op and the strict width check passes.
func TestHTTPEmbedderEndpointHonorsDimensions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		vec := make([]float32, req.Dimensions) // honor the requested width
		for i := range vec {
			vec[i] = 0.02
		}
		_ = json.NewEncoder(w).Encode(embedResp{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{{Embedding: vec}},
		})
	}))
	defer srv.Close()

	e := NewHTTPEmbedder(srv.URL, "route-openrouter-embedding-fallback", 1024, "")
	vec, err := e.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(vec) != EmbeddingDimension {
		t.Errorf("len = %d, want %d", len(vec), EmbeddingDimension)
	}
}

// TestHTTPEmbedderStrictRejectByDefault confirms reduceTo=0 (unset) still
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

	e := NewHTTPEmbedder(srv.URL, "bge-m3", 0, "")
	if _, err := e.Embed(context.Background(), "hello"); err == nil {
		t.Fatal("expected dimension-mismatch error, got nil")
	}
}

// TestHTTPEmbedderSendsAuthHeaderWhenKeySet verifies the LiteLLM master key
// is sent as a Bearer token so /v1/embeddings does not 401.
func TestHTTPEmbedderSendsAuthHeaderWhenKeySet(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		vec := make([]float32, EmbeddingDimension)
		_ = json.NewEncoder(w).Encode(embedResp{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{{Embedding: vec}},
		})
	}))
	defer srv.Close()

	e := NewHTTPEmbedder(srv.URL, "bge-m3", 0, "sk-test-key")
	if _, err := e.Embed(context.Background(), "hello"); err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if gotAuth != "Bearer sk-test-key" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer sk-test-key")
	}
}

// TestHTTPEmbedderOmitsAuthHeaderWhenKeyEmpty verifies a local backend
// (e.g. Ollama) that needs no auth does not receive a bogus header.
func TestHTTPEmbedderOmitsAuthHeaderWhenKeyEmpty(t *testing.T) {
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization") != ""
		vec := make([]float32, EmbeddingDimension)
		_ = json.NewEncoder(w).Encode(embedResp{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{{Embedding: vec}},
		})
	}))
	defer srv.Close()

	e := NewHTTPEmbedder(srv.URL, "bge-m3", 0, "")
	if _, err := e.Embed(context.Background(), "hello"); err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if sawAuth {
		t.Error("expected no Authorization header when apiKey is empty")
	}
}
