package inference

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// paddedBody returns prefix with a trailing "_pad" string value sized so the
// full JSON body (prefix + pad + closing `"}`) is exactly n bytes. Built by
// direct string concatenation rather than json.Marshal so the byte count is
// exact -- these tests must land precisely on apierrors.MaxRequestBodyBytes and
// apierrors.MaxRequestBodyBytes+1, and Go map-key marshal order plus re-encoding
// overhead would make that imprecise.
func paddedBody(t *testing.T, prefix string, n int) string {
	t.Helper()
	const suffix = `"}`
	padLen := n - len(prefix) - len(suffix)
	if padLen < 0 {
		t.Fatalf("target size %d too small for prefix/suffix overhead (%d)", n, len(prefix)+len(suffix))
	}
	return prefix + strings.Repeat("x", padLen) + suffix
}

// requestTooLargeBody asserts the honest-413 shape issue #1250 requires: the
// real HTTP status, a message that names the limit, and no trace of the old
// lying "invalid JSON" / "Invalid request body" wording that truncation used
// to produce for a body that was never actually malformed.
func requestTooLargeBody(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v (%s)", err, w.Body.String())
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got: %s", w.Body.String())
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "MiB") {
		t.Fatalf("message must name the limit, got: %q", msg)
	}
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "json") {
		t.Fatalf("oversized body must never be reported as an invalid-JSON error, got: %q", msg)
	}
}

// The four "OneByteUnderIsAccepted" tests below send exactly
// apierrors.MaxRequestBodyBytes bytes, not one byte under it: http.MaxBytesReader
// accepts exactly n and rejects the (n+1)th byte, so the real boundary is n
// and n+1. Testing at n-1 would leave a one-byte hole where an off-by-one
// that started rejecting exactly at the limit (instead of one past it)
// would pass every test in this file.
func TestBodyLimit_ChatCompletions_OneByteUnderIsAccepted(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	prefix := `{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"_pad":"`
	body := paddedBody(t, prefix, apierrors.MaxRequestBodyBytes)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	// 401 = reached the auth layer, i.e. the body parsed and passed field
	// validation instead of being rejected as too large or malformed.
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (auth layer reached), got %d: %s", w.Code, w.Body.String())
	}
}

func TestBodyLimit_ChatCompletions_OneByteOverIsHonest413(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	prefix := `{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"_pad":"`
	body := paddedBody(t, prefix, apierrors.MaxRequestBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	requestTooLargeBody(t, w)
}

func TestBodyLimit_Completions_OneByteUnderIsAccepted(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	prefix := `{"model":"hive-fast","prompt":"hi","_pad":"`
	body := paddedBody(t, prefix, apierrors.MaxRequestBodyBytes)
	req := httptest.NewRequest(http.MethodPost, "/v1/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (auth layer reached), got %d: %s", w.Code, w.Body.String())
	}
}

func TestBodyLimit_Completions_OneByteOverIsHonest413(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	prefix := `{"model":"hive-fast","prompt":"hi","_pad":"`
	body := paddedBody(t, prefix, apierrors.MaxRequestBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/v1/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	requestTooLargeBody(t, w)
}

func TestBodyLimit_Embeddings_OneByteUnderIsAccepted(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	prefix := `{"model":"hive-embedding-default","input":"hi","_pad":"`
	body := paddedBody(t, prefix, apierrors.MaxRequestBodyBytes)
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (auth layer reached), got %d: %s", w.Code, w.Body.String())
	}
}

func TestBodyLimit_Embeddings_OneByteOverIsHonest413(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	prefix := `{"model":"hive-embedding-default","input":"hi","_pad":"`
	body := paddedBody(t, prefix, apierrors.MaxRequestBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	requestTooLargeBody(t, w)
}

func TestBodyLimit_Responses_OneByteUnderIsAccepted(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	prefix := `{"model":"hive-fast","input":"hi","_pad":"`
	body := paddedBody(t, prefix, apierrors.MaxRequestBodyBytes)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (auth layer reached), got %d: %s", w.Code, w.Body.String())
	}
}

func TestBodyLimit_Responses_OneByteOverIsHonest413(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	prefix := `{"model":"hive-fast","input":"hi","_pad":"`
	body := paddedBody(t, prefix, apierrors.MaxRequestBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	requestTooLargeBody(t, w)
}

// TestBodyLimit_ChatCompletions_ContentLengthOverLimit_RejectedWithoutReading
// proves the declared-oversize fast path fires before any body bytes are
// read: the actual body here is small, only the Content-Length header lies.
func TestBodyLimit_ChatCompletions_ContentLengthOverLimit_RejectedWithoutReading(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}]}`))
	req.ContentLength = apierrors.MaxRequestBodyBytes + 1
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	requestTooLargeBody(t, w)
}

// TestBodyLimit_TrustedBody_SkipsTheCap proves apierrors.WithTrustedBody
// (set by the /v1/messages surface on its translated sub-request, PR #1273
// review finding 2) makes readLimitedBody skip MaxRequestBodyBytes
// entirely: a body over the cap is still read and reaches field validation
// instead of being refused as too large.
func TestBodyLimit_TrustedBody_SkipsTheCap(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	prefix := `{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"_pad":"`
	body := paddedBody(t, prefix, apierrors.MaxRequestBodyBytes+1024)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req = req.WithContext(apierrors.WithTrustedBody(req.Context()))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("a trusted body must not be capped: got 413: %s", w.Body.String())
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (auth layer reached, body read in full), got %d: %s", w.Code, w.Body.String())
	}
}
