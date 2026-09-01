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

// TestHTTPEmbedderNeverRequestsDimensions is the regression guard for the
// LiteLLM 400: its generic openai adapter rejects `dimensions` for any model
// name without "text-embedding-3" and ignores drop_params, so sending the
// parameter failed every embedding call. The narrower width must be reached by
// reducing the native-width response client-side instead.
func TestHTTPEmbedderNeverRequestsDimensions(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		vec := make([]float32, 4096) // native width, as the provider serves it
		for i := range vec {
			vec[i] = 0.01
		}
		_ = json.NewEncoder(w).Encode(embedResp{
			Data: []embedVector{{Embedding: vec}},
		})
	}))
	defer srv.Close()

	e := NewHTTPEmbedder(srv.URL, "route-openrouter-embedding-fallback", 1024, "")
	vec, err := e.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if _, present := gotBody["dimensions"]; present {
		t.Error("request body carried a dimensions field; LiteLLM's openai adapter 400s on it")
	}
	if len(vec) != EmbeddingDimension {
		t.Errorf("len = %d, want %d", len(vec), EmbeddingDimension)
	}
}

// TestHTTPEmbedderAcceptsBackendAtTargetWidth covers a backend that already
// serves the model at the configured width (a local bge-m3 endpoint, or a
// provider serving Qwen3 reduced): the client-side reduce is a no-op and the
// strict width check passes.
func TestHTTPEmbedderAcceptsBackendAtTargetWidth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vec := make([]float32, EmbeddingDimension)
		for i := range vec {
			vec[i] = 0.02
		}
		_ = json.NewEncoder(w).Encode(embedResp{
			Data: []embedVector{{Embedding: vec}},
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
			Data: []embedVector{{Embedding: vec}},
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
			Data: []embedVector{{Embedding: vec}},
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
			Data: []embedVector{{Embedding: vec}},
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

// The response read ceiling must be sized off the width that crosses the wire,
// not off the post-reduction width. On an MRL deployment reduceEmbedding runs
// client side on a native-width vector, so a backend serving 3584 or 4096
// natively sends that much regardless of EmbeddingDimension being 1024. A
// ceiling sized on the reduced width truncates a 256 chunk page at the
// LimitReader, the decode returns an unexpected EOF, and every large web_fetch
// on that deployment fails permanently and indistinguishably from an outage.
//
// Asserted arithmetically rather than with an eleven megabyte fixture: the
// property is the sizing, and the sizing is a function.
func TestEmbedResponseCeilingCoversNativeWidthBatches(t *testing.T) {
	// Measured bytes per dimension for a JSON float32 plus its comma, rounded
	// up from about 14. The ceiling has to clear this in the worst case.
	const measuredBytesPerDim = 15

	restore := EmbeddingDimension
	t.Cleanup(func() { EmbeddingDimension = restore })
	// The reduced width an MRL deployment configures, which is what the first
	// version of this ceiling was sized on.
	EmbeddingDimension = 1024

	for _, nativeDim := range []int{1024, 2048, 3584, 4096} {
		for _, n := range []int{1, 128, 256} {
			need := int64(n) * int64(nativeDim) * measuredBytesPerDim
			if got := embedResponseCeiling(n); got < need {
				t.Fatalf("ceiling for %d inputs = %d bytes, need at least %d for a native width of %d",
					n, got, need, nativeDim)
			}
		}
	}

	// And the fixed 4 MiB this replaced would not have: a 256 chunk page at
	// 1024 native is about 2.9 MB and fits, but the same page at 4096 native
	// is about 11.7 MB and does not. Keeps the reason for the change attached
	// to the test that enforces it.
	if embedResponseCeiling(256) <= 4*1024*1024 {
		t.Fatal("the ceiling did not grow past the fixed 4 MiB it replaced")
	}
}
