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
	unwrapped     bool
}

func newCaptureHandler() (http.Handler, *capturedRequest) {
	captured := &capturedRequest{}
	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured.authorization = r.Header.Get("Authorization")
		captured.unwrapped = auth.IsOWUIUnwrapped(r.Context())
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
	if !captured.unwrapped {
		t.Fatalf("expected IsOWUIUnwrapped(ctx) true after a successful rewrite")
	}
}

// TestOWUIUnwrap_NoShimKey_DoesNotMarkUnwrapped guards the #269 tenant_id
// fallback boundary: a request that never presented the shim key (real
// API key, unrelated JWT) must never carry the OWUI-unwrapped marker,
// however it got here -- JWTMiddleware's DB fallback is gated on it.
func TestOWUIUnwrap_NoShimKey_DoesNotMarkUnwrapped(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	req := wrap(t, map[string]any{"model": "gpt-4o"}, "Bearer hk_real_api_key")
	mw(next).ServeHTTP(httptest.NewRecorder(), req)
	if captured.unwrapped {
		t.Fatalf("non-shim request must not be marked unwrapped")
	}
}

// TestOWUIUnwrap_ShimKeyWithoutMetadata_DoesNotMarkUnwrapped covers the
// no-token fall-through path: the shim key was presented but no
// upstream_auth was injected, so nothing was actually rewritten.
func TestOWUIUnwrap_ShimKeyWithoutMetadata_DoesNotMarkUnwrapped(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	req := wrap(t, map[string]any{"model": "gpt-4o"}, "Bearer "+testShimKey)
	mw(next).ServeHTTP(httptest.NewRecorder(), req)
	if captured.unwrapped {
		t.Fatalf("no-metadata fall-through must not be marked unwrapped")
	}
}

func TestOWUIUnwrap_StripsEntireMetadataIncludingNonAuthKeys(t *testing.T) {
	// Defence in depth: even non-auth __metadata fields must be stripped
	// so OWUI-internal fields never leak to downstream handlers/sinks.
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	body := map[string]any{
		"model": "gpt-4o",
		"__metadata": map[string]any{
			"upstream_auth": "Bearer tok",
			"trace_id":      "abc",
			"chat_id":       "xyz",
		},
	}
	req := wrap(t, body, "Bearer "+testShimKey)
	mw(next).ServeHTTP(httptest.NewRecorder(), req)

	if bytes.Contains(captured.body, []byte("__metadata")) {
		t.Fatalf("expected __metadata fully stripped, got %s", captured.body)
	}
	if bytes.Contains(captured.body, []byte("trace_id")) || bytes.Contains(captured.body, []byte("chat_id")) {
		t.Fatalf("expected non-auth metadata also stripped: %s", captured.body)
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

func TestOWUIUnwrap_NonJSONContentType_PassesThrough(t *testing.T) {
	// Multipart (audio/image uploads) and other non-JSON content types
	// legitimately reach the API-key path with the shim key; we must
	// not buffer + reject their bodies.
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions",
		bytes.NewReader([]byte("multipart-body")))
	req.Header.Set("Authorization", "Bearer "+testShimKey)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=abc")
	mw(next).ServeHTTP(httptest.NewRecorder(), req)
	if captured.authorization != "Bearer "+testShimKey {
		t.Fatalf("non-JSON shim must pass through, got %q", captured.authorization)
	}
	if !bytes.Equal(captured.body, []byte("multipart-body")) {
		t.Fatalf("non-JSON body must be preserved, got %q", captured.body)
	}
}

func TestOWUIUnwrap_JSONContentTypeWithParams_Recognised(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	body := map[string]any{"__metadata": map[string]any{"upstream_auth": "Bearer jwt-x"}}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+testShimKey)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	mw(next).ServeHTTP(httptest.NewRecorder(), req)
	if captured.authorization != "Bearer jwt-x" {
		t.Fatalf("application/json with charset must be recognised, got %q", captured.authorization)
	}
}

func TestOWUIUnwrap_OverLongToken_Rejects401(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, _ := newCaptureHandler()
	bigToken := strings.Repeat("a", (8<<10)+1) // > 8 KiB
	body := map[string]any{
		"__metadata": map[string]any{"upstream_auth": "Bearer " + bigToken},
	}
	req := wrap(t, body, "Bearer "+testShimKey)
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on oversized token, got %d", rr.Code)
	}
}

func TestOWUIUnwrap_StripsMalformedMetadata(t *testing.T) {
	// A __metadata value that isn't an object must still be stripped —
	// never forward an opaque field to downstream handlers.
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	req := wrap(t, []byte(`{"model":"x","__metadata":"not-an-object"}`), "Bearer "+testShimKey)
	mw(next).ServeHTTP(httptest.NewRecorder(), req)
	if bytes.Contains(captured.body, []byte("__metadata")) {
		t.Fatalf("malformed __metadata must still be stripped: %s", captured.body)
	}
}
