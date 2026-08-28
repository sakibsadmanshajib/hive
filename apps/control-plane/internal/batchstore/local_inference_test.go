package batchstore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestChatCompletion_TruncatedResponseIsAnErrorNotSuccess -- issue #1255
// finding #2: the local batch executor persisted a possibly-truncated
// completion as a successful result. ChatCompletion read the upstream body
// through a plain io.LimitReader, which truncates silently rather than
// erroring, so a response larger than the cap was returned as a normal
// success with a cut-off body. A batch line whose completion exceeds this
// cap would then write truncated, likely-invalid JSON into the customer's
// batch output file marked as a success, with nothing recording that
// truncation occurred. This test proves the client now detects that case
// and returns an error instead of a truncated success.
func TestChatCompletion_TruncatedResponseIsAnErrorNotSuccess(t *testing.T) {
	oversized := strings.Repeat("a", maxLocalInferenceResponseBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Not valid JSON on its own -- the point is the client must reject
		// this on SIZE alone, before any JSON-parse step ever runs.
		w.Write([]byte(`{"id":"chatcmpl-x","content":"` + oversized + `"}`))
	}))
	defer srv.Close()

	client := NewLiteLLMInferenceClient(srv.URL, "test-key")
	body, usage, status, err := client.ChatCompletion(context.Background(), "route-x", json.RawMessage(`{"model":"route-x"}`))
	if err == nil {
		t.Fatalf("expected an error for an oversized response, got success (status=%d, usage=%+v)", status, usage)
	}
	if body != nil {
		t.Fatalf("expected nil body on a truncation error, got %d bytes", len(body))
	}
}

// TestChatCompletion_WithinCapSucceeds is the green-path control: a normal
// response under the cap is unaffected by the new size check.
func TestChatCompletion_WithinCapSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"chatcmpl-x","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	client := NewLiteLLMInferenceClient(srv.URL, "test-key")
	body, usage, status, err := client.ChatCompletion(context.Background(), "route-x", json.RawMessage(`{"model":"route-x"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 200 {
		t.Fatalf("status=%d want 200", status)
	}
	if usage == nil || usage.TotalTokens != 2 {
		t.Fatalf("usage=%+v want total_tokens=2", usage)
	}
	if len(body) == 0 {
		t.Fatalf("expected non-empty body")
	}
}

// TestChatCompletion_ExactlyAtCapSucceeds is the boundary control PR #1253's
// review named (finding M1): the read limit is maxLocalInferenceResponseBytes+1
// and the truncation check is `len(respBody) > maxLocalInferenceResponseBytes`,
// two off-by-one-prone numbers that must agree. A body of EXACTLY the cap
// size must succeed, not be misreported as truncated.
func TestChatCompletion_ExactlyAtCapSucceeds(t *testing.T) {
	// Build a JSON body whose total byte length is exactly
	// maxLocalInferenceResponseBytes, padding a string field to make up the
	// difference.
	const prefix = `{"id":"chatcmpl-x","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},"padding":"`
	const suffix = `"}`
	padLen := maxLocalInferenceResponseBytes - len(prefix) - len(suffix)
	if padLen < 0 {
		t.Fatalf("prefix+suffix already exceeds the cap, fixture needs adjusting")
	}
	body := prefix + strings.Repeat("a", padLen) + suffix
	if len(body) != maxLocalInferenceResponseBytes {
		t.Fatalf("fixture body is %d bytes, want exactly %d", len(body), maxLocalInferenceResponseBytes)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	}))
	defer srv.Close()

	client := NewLiteLLMInferenceClient(srv.URL, "test-key")
	respBody, usage, status, err := client.ChatCompletion(context.Background(), "route-x", json.RawMessage(`{"model":"route-x"}`))
	if err != nil {
		t.Fatalf("exactly-at-cap body reported as truncated: %v", err)
	}
	if status != 200 {
		t.Fatalf("status=%d want 200", status)
	}
	if usage == nil || usage.TotalTokens != 2 {
		t.Fatalf("usage=%+v want total_tokens=2", usage)
	}
	if len(respBody) != maxLocalInferenceResponseBytes {
		t.Fatalf("returned body is %d bytes, want exactly %d", len(respBody), maxLocalInferenceResponseBytes)
	}
}
