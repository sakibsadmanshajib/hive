package auth_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

const testShimKey = "owui-shim-key"

type capturedRequest struct {
	authorization string
	body          []byte
}

func newCaptureHandler() (http.Handler, *capturedRequest) {
	captured := &capturedRequest{}
	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured.authorization = r.Header.Get("Authorization")
		if r.Body != nil {
			captured.body, _ = io.ReadAll(r.Body)
			_ = r.Body.Close()
		}
	})
	return h, captured
}

func wrap(t *testing.T, body any, header string) *http.Request {
	t.Helper()
	var b []byte
	switch v := body.(type) {
	case nil:
		b = nil
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		var err error
		b, err = json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestOWUIUnwrap_RewritesShimKeyToJWT(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	body := map[string]any{
		"model": "gpt-4o",
		"messages": []map[string]string{
			{"role": "user", "content": "hi"},
		},
		"__metadata": map[string]any{
			"upstream_auth": "Bearer tenant-jwt-abc",
		},
	}
	req := wrap(t, body, "Bearer "+testShimKey)
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 forwarded, got %d %s", rr.Code, rr.Body.String())
	}
	if captured.authorization != "Bearer tenant-jwt-abc" {
		t.Fatalf("expected JWT swap, got %q", captured.authorization)
	}
	var got map[string]any
	if err := json.Unmarshal(captured.body, &got); err != nil {
		t.Fatalf("downstream body invalid json: %v", err)
	}
	if _, present := got["__metadata"]; present {
		t.Fatalf("expected __metadata stripped, got %v", got["__metadata"])
	}
	if _, ok := got["messages"]; !ok {
		t.Fatalf("expected messages preserved: %v", got)
	}
}

func TestOWUIUnwrap_PreservesUnrelatedMetadata(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	body := map[string]any{
		"model": "gpt-4o",
		"__metadata": map[string]any{
			"upstream_auth": "Bearer tok",
			"trace_id":      "abc",
		},
	}
	req := wrap(t, body, "Bearer "+testShimKey)
	mw(next).ServeHTTP(httptest.NewRecorder(), req)

	var got map[string]any
	if err := json.Unmarshal(captured.body, &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	meta, ok := got["__metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected __metadata preserved with non-auth keys, got %v", got)
	}
	if _, present := meta["upstream_auth"]; present {
		t.Fatalf("upstream_auth must be stripped from __metadata: %v", meta)
	}
	if meta["trace_id"] != "abc" {
		t.Fatalf("expected trace_id preserved: %v", meta)
	}
}

func TestOWUIUnwrap_NoShimKey_PassesThrough(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	body := map[string]any{"model": "gpt-4o", "__metadata": map[string]any{"upstream_auth": "Bearer x"}}
	req := wrap(t, body, "Bearer hk_real_api_key")
	mw(next).ServeHTTP(httptest.NewRecorder(), req)

	if captured.authorization != "Bearer hk_real_api_key" {
		t.Fatalf("expected API key preserved, got %q", captured.authorization)
	}
	if !bytes.Contains(captured.body, []byte("upstream_auth")) {
		t.Fatalf("expected body untouched when not shim, got %s", captured.body)
	}
}

func TestOWUIUnwrap_DisabledWhenShimKeyEmpty(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: ""})
	next, captured := newCaptureHandler()
	body := map[string]any{"__metadata": map[string]any{"upstream_auth": "Bearer x"}}
	req := wrap(t, body, "Bearer anything")
	mw(next).ServeHTTP(httptest.NewRecorder(), req)
	if captured.authorization != "Bearer anything" {
		t.Fatalf("disabled mw must pass through, got %q", captured.authorization)
	}
}

func TestOWUIUnwrap_ShimKeyWithoutMetadata_FallsThrough(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	body := map[string]any{"model": "gpt-4o"}
	req := wrap(t, body, "Bearer "+testShimKey)
	mw(next).ServeHTTP(httptest.NewRecorder(), req)

	if captured.authorization != "Bearer "+testShimKey {
		t.Fatalf("no metadata → shim key must remain (selector will reject), got %q", captured.authorization)
	}
}

func TestOWUIUnwrap_InvalidJSON_FallsThrough(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	req := wrap(t, []byte("not json"), "Bearer "+testShimKey)
	mw(next).ServeHTTP(httptest.NewRecorder(), req)
	if captured.authorization != "Bearer "+testShimKey {
		t.Fatalf("non-json must fall through, got %q", captured.authorization)
	}
}

func TestOWUIUnwrap_OverLimitBody_413(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, _ := newCaptureHandler()
	big := bytes.Repeat([]byte("a"), (2<<20)+10) // 2 MiB + 10
	req := wrap(t, big, "Bearer "+testShimKey)
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rr.Code)
	}
}

func TestOWUIUnwrap_RawTokenWithoutBearerPrefix(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	body := map[string]any{
		"__metadata": map[string]any{"upstream_auth": "raw-jwt-no-scheme"},
	}
	req := wrap(t, body, "Bearer "+testShimKey)
	mw(next).ServeHTTP(httptest.NewRecorder(), req)
	if captured.authorization != "Bearer raw-jwt-no-scheme" {
		t.Fatalf("expected normalized Bearer, got %q", captured.authorization)
	}
}

func TestOWUIUnwrap_CaseInsensitiveScheme(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	body := map[string]any{
		"__metadata": map[string]any{"upstream_auth": "Bearer jwt"},
	}
	req := wrap(t, body, "bearer "+testShimKey)
	mw(next).ServeHTTP(httptest.NewRecorder(), req)
	if captured.authorization != "Bearer jwt" {
		t.Fatalf("expected lowercase scheme accepted, got %q", captured.authorization)
	}
}

func TestOWUIUnwrap_GETRequestPassesThrough(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+testShimKey)
	mw(next).ServeHTTP(httptest.NewRecorder(), req)
	if !strings.EqualFold(captured.authorization, "Bearer "+testShimKey) {
		t.Fatalf("GET no-body should not rewrite, got %q", captured.authorization)
	}
}
