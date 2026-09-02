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
	return wrapPath(t, "/v1/chat/completions", body, header)
}

// wrapPath builds the same JSON request as wrap for an arbitrary path.
// Path matters since the unwrap middleware fails closed on the chat
// completions path (the only path the hive_jwt_forward pipeline owns) and
// passes through on the paths where the shim key is itself the intended
// credential.
func wrapPath(t *testing.T, path string, body any, header string) *http.Request {
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
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
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
// upstream_auth was injected, so nothing was actually rewritten. Asserted
// on the embeddings path because that is where the fall-through is
// legitimate; the chat completions path now fails closed instead (see
// TestOWUIUnwrap_ChatCompletionsWithoutMetadata_RejectsWithRealReason).
func TestOWUIUnwrap_ShimKeyWithoutMetadata_DoesNotMarkUnwrapped(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	req := wrapPath(t, "/v1/embeddings", map[string]any{"model": "hive-embedding-default"}, "Bearer "+testShimKey)
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

// TestOWUIUnwrap_ShimKeyWithoutMetadata_FallsThrough documents that on the
// paths where the shim key is itself the intended credential a missing
// __metadata is expected and the shim key must survive to the API-key path.
//
// The list is now audio alone. /v1/embeddings used to be here too, and that is
// exactly what issue #1696 records: OWUI's Python retrieval path spends real,
// metered credits on this gateway, and letting the shim key survive there put
// every tenant's search, ingest and query embeddings on one platform account.
// Text-to-speech and speech-to-text still do, which is named in
// requiresPerUserAuth rather than left to be rediscovered.
func TestOWUIUnwrap_ShimKeyWithoutMetadata_FallsThrough(t *testing.T) {
	for _, path := range []string{"/v1/audio/speech", "/v1/audio/transcriptions"} {
		t.Run(path, func(t *testing.T) {
			mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
			next, captured := newCaptureHandler()
			req := wrapPath(t, path, map[string]any{"model": "hive-tts"}, "Bearer "+testShimKey)
			rr := httptest.NewRecorder()
			mw(next).ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("shim-authenticated %s must not be rejected, got %d %s", path, rr.Code, rr.Body.String())
			}
			if captured.authorization != "Bearer "+testShimKey {
				t.Fatalf("no metadata → shim key must remain on %s, got %q", path, captured.authorization)
			}
		})
	}
}

func TestOWUIUnwrap_InvalidJSON_FallsThrough(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	req := wrapPath(t, "/v1/audio/speech", []byte("not json"), "Bearer "+testShimKey)
	mw(next).ServeHTTP(httptest.NewRecorder(), req)
	if captured.authorization != "Bearer "+testShimKey {
		t.Fatalf("non-json must fall through, got %q", captured.authorization)
	}
}

// TestOWUIUnwrap_InvalidJSONOnEmbeddings_StillRejects closes the same way
// around the #1696 refusal that TestOWUIUnwrap_ChatCompletionsWithNonJSONBody_StillRejects
// closes on the chat path: a body the unwrap cannot parse carries no token, so
// forwarding it would put the spend back on the shim account.
func TestOWUIUnwrap_InvalidJSONOnEmbeddings_StillRejects(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	req := wrapPath(t, "/v1/embeddings", []byte("not json"), "Bearer "+testShimKey)
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	if captured.authorization != "" {
		t.Fatalf("request must not reach the handler, got authorization %q", captured.authorization)
	}
}

// TestOWUIUnwrap_ChatCompletionsWithoutMetadata_RejectsWithRealReason pins
// the honest-failure behaviour. A shim-key chat completion with no
// __metadata means the hive_jwt_forward Function is missing or broken.
// Forwarding it either mis-attributes the completion to the shim account
// (when the shim key resolves) or produces a misleading "Incorrect API key
// provided" (when it does not). Both are wrong, so reject here and name
// the real cause.
func TestOWUIUnwrap_ChatCompletionsWithoutMetadata_RejectsWithRealReason(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	req := wrap(t, map[string]any{"model": "hive-default"}, "Bearer "+testShimKey)
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	if captured.authorization != "" {
		t.Fatalf("request must not reach the handler, got authorization %q", captured.authorization)
	}
	body := rr.Body.String()
	if strings.Contains(strings.ToLower(body), "incorrect api key") {
		t.Fatalf("must not report the generic invalid-key reason, got %s", body)
	}
	// The operator-facing reason must name the actual missing piece.
	for _, want := range []string{"session", "sign in"} {
		if !strings.Contains(strings.ToLower(body), want) {
			t.Fatalf("expected reason mentioning %q, got %s", want, body)
		}
	}
}

// TestOWUIUnwrap_ChatCompletionsWithNonJSONBody_StillRejects closes the way
// around the rejection above. The non-JSON pass-through exists for multipart
// audio and image uploads, but on the chat completions path it used to run
// first, so a shim-key POST that simply omitted Content-Type skipped the
// fail-closed check entirely and reached the API-key path untenanted. A
// per-user token can only arrive in a JSON __metadata block, so neither of
// these requests can ever be legitimate here.
func TestOWUIUnwrap_ChatCompletionsWithNonJSONBody_StillRejects(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
	}{
		{"absent content type", ""},
		{"multipart content type", "multipart/form-data; boundary=abc"},
		{"text content type", "text/plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
			next, captured := newCaptureHandler()
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
				bytes.NewReader([]byte(`{"model":"hive-default"}`)))
			req.Header.Set("Authorization", "Bearer "+testShimKey)
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			} else {
				req.Header.Del("Content-Type")
			}
			rr := httptest.NewRecorder()
			mw(next).ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
			}
			if captured.authorization != "" {
				t.Fatalf("request must not reach the handler, got authorization %q", captured.authorization)
			}
			if strings.Contains(strings.ToLower(rr.Body.String()), "incorrect api key") {
				t.Fatalf("must not report the generic invalid-key reason, got %s", rr.Body.String())
			}
		})
	}
}

func TestOWUIUnwrap_OverLimitBody_413(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, _ := newCaptureHandler()
	big := bytes.Repeat([]byte("a"), (16<<20)+10) // 16 MiB + 10
	req := wrap(t, big, "Bearer "+testShimKey)
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rr.Code)
	}
}

// A chat body carrying inlined attachment text (#1108) is legitimately
// multiple MiB. Just under the new cap it must flow through with the
// per-user token still unwrapped from __metadata.
func TestOWUIUnwrap_LargeInlinedBodyWithinCap_Unwraps(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	filler := string(bytes.Repeat([]byte("a"), (12<<20)-1024))
	raw := `{"model":"hive-default","messages":[{"role":"user","content":"` + filler + `"}],"__metadata":{"upstream_auth":"user-jwt"}}`
	req := wrap(t, []byte(raw), "Bearer "+testShimKey)
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", rr.Code, rr.Body.String())
	}
	if captured.authorization != "Bearer user-jwt" {
		t.Fatalf("expected per-user token on a ~12 MiB body, got %q", captured.authorization)
	}
}

// A declared oversize Content-Length is refused before the body is read,
// so the client learns immediately instead of uploading megabytes into a
// request that was already doomed (#1108 silent-hang shape).
func TestOWUIUnwrap_ContentLengthOverLimit_RejectedWithoutReading(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	body := strings.NewReader(`{"model":"hive-default"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	req.Header.Set("Authorization", "Bearer "+testShimKey)
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = (16 << 20) + 1
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", rr.Code)
	}
	if captured.authorization != "" {
		t.Fatalf("handler must not run, got authorization %q", captured.authorization)
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

// TestOWUIUnwrap_GETRequestPassesThrough documents that a bodyless GET has no
// __metadata to lift, so this middleware cannot rewrite it and leaves the shim
// key on the header. Downstream authorization then resolves that key like any
// other API key, which is why OWUI_SHIM_KEY has to be a real minted key.
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
	req := wrapPath(t, "/v1/embeddings", []byte(`{"model":"x","__metadata":"not-an-object"}`), "Bearer "+testShimKey)
	mw(next).ServeHTTP(httptest.NewRecorder(), req)
	if bytes.Contains(captured.body, []byte("__metadata")) {
		t.Fatalf("malformed __metadata must still be stripped: %s", captured.body)
	}
}

// TestOWUIUnwrap_EmbeddingsWithoutUserToken_Rejects is the fail-closed half of
// issue #1696. Open WebUI's Python retrieval path (web search indexing,
// document ingest, retrieval queries) posts to /v1/embeddings with
// RAG_OPENAI_API_KEY, which is OWUI_SHIM_KEY. Every one of those calls used to
// be authorized, held and charged against whichever account owns that key, so
// one platform account absorbed the embedding spend of every tenant at once
// and no customer's own usage showed the search they ran.
//
// The rule this asserts is not "charge someone else instead". It is that a
// call which cannot say who it belongs to is refused before it reaches the
// handler, so no request can quietly fall back to billing the platform.
func TestOWUIUnwrap_EmbeddingsWithoutUserToken_Rejects(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	req := wrapPath(t, "/v1/embeddings",
		map[string]any{"model": "hive-embedding-default", "input": []string{"hello"}},
		"Bearer "+testShimKey)
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	if captured.authorization != "" {
		t.Fatalf("request must not reach the handler, got authorization %q", captured.authorization)
	}
	if strings.Contains(strings.ToLower(rr.Body.String()), "incorrect api key") {
		t.Fatalf("must not report the generic invalid-key reason, got %s", rr.Body.String())
	}
}

// TestOWUIUnwrap_EmbeddingsWithUserToken_RewritesToTheUser is the other half:
// the decorated call is rewritten onto the signed-in user's own credential, so
// everything downstream (route selection, the credit hold, the charge, the
// usage row) binds to that user rather than to the shim principal.
func TestOWUIUnwrap_EmbeddingsWithUserToken_RewritesToTheUser(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	req := wrapPath(t, "/v1/embeddings",
		map[string]any{"model": "hive-embedding-default", "input": []string{"hello"}},
		"Bearer "+testShimKey)
	req.Header.Set(auth.UpstreamAuthHeader, "Bearer searching-user-jwt")
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if captured.authorization != "Bearer searching-user-jwt" {
		t.Fatalf("expected the searching user's token on Authorization, got %q", captured.authorization)
	}
	if strings.Contains(captured.authorization, testShimKey) {
		t.Fatalf("the shim key must not survive onto the forwarded request")
	}
	if !captured.unwrapped {
		t.Fatalf("expected IsOWUIUnwrapped(ctx) true so the tenant fallback applies")
	}
}

// TestOWUIUnwrap_EmbeddingsWithARealAPIKey_PassesThrough keeps the blast
// radius of the change above where it belongs. requiresPerUserAuth is only
// ever consulted under the shim key, so a paying customer calling
// /v1/embeddings with their own hk_ key is untouched by #1696.
func TestOWUIUnwrap_EmbeddingsWithARealAPIKey_PassesThrough(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCaptureHandler()
	req := wrapPath(t, "/v1/embeddings",
		map[string]any{"model": "hive-embedding-default", "input": []string{"hello"}},
		"Bearer hk_customer_key")
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected pass-through, got %d: %s", rr.Code, rr.Body.String())
	}
	if captured.authorization != "Bearer hk_customer_key" {
		t.Fatalf("expected the customer's own key untouched, got %q", captured.authorization)
	}
}
