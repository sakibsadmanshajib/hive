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

// The header carrier exists because three of the four agent-task calls are
// bodyless -- GET /v1/agent/tasks, GET /v1/agent/tasks/{id} and the bodyless
// POST .../cancel (apps/edge-api/internal/agenttask/handler.go:38-72) -- so
// __metadata.upstream_auth, which only a JSON body can carry, cannot reach
// them at all. It sits behind exactly the same gate as the body carrier: the
// inbound Authorization must be the shim key and nothing else.

type carrierCapture struct {
	authorization string
	carrier       string
	carrierSeen   bool
	unwrapped     bool
	served        bool
}

func newCarrierHandler() (http.Handler, *carrierCapture) {
	captured := &carrierCapture{}
	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured.served = true
		captured.authorization = r.Header.Get("Authorization")
		captured.carrier = r.Header.Get(auth.UpstreamAuthHeader)
		_, captured.carrierSeen = r.Header[http.CanonicalHeaderKey(auth.UpstreamAuthHeader)]
		captured.unwrapped = auth.IsOWUIUnwrapped(r.Context())
	})
	return h, captured
}

func carrierRequest(method, path, authorization, carrier string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if carrier != "" {
		req.Header.Set(auth.UpstreamAuthHeader, carrier)
	}
	return req
}

func TestOWUIUnwrap_HeaderCarrierUnderShimKeyRewritesAuthorization(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCarrierHandler()

	req := carrierRequest(http.MethodGet, "/v1/agent/tasks", "Bearer "+testShimKey, "Bearer user-jwt")
	mw(next).ServeHTTP(httptest.NewRecorder(), req)

	if captured.authorization != "Bearer user-jwt" {
		t.Fatalf("authorization = %q, want the per-user token", captured.authorization)
	}
	if !captured.unwrapped {
		t.Fatal("request must be marked unwrapped so the tenant fallback applies")
	}
	if captured.carrierSeen {
		t.Fatalf("%s must never reach a handler, got %q", auth.UpstreamAuthHeader, captured.carrier)
	}
}

func TestOWUIUnwrap_HeaderCarrierAcceptsBareToken(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCarrierHandler()

	req := carrierRequest(http.MethodGet, "/v1/agent/tasks", "Bearer "+testShimKey, "user-jwt")
	mw(next).ServeHTTP(httptest.NewRecorder(), req)

	if captured.authorization != "Bearer user-jwt" {
		t.Fatalf("authorization = %q, want the bare token promoted to a Bearer credential", captured.authorization)
	}
}

// Without the shim key the carrier is worthless and must also be unreadable:
// a handler that could see it is a handler that could be taught to trust it.
func TestOWUIUnwrap_HeaderCarrierIgnoredAndStrippedWithoutShimKey(t *testing.T) {
	for name, authorization := range map[string]string{
		"api key":          "Bearer hk_live_real_key",
		"real jwt":         "Bearer some.other.jwt",
		"no authorization": "",
	} {
		t.Run(name, func(t *testing.T) {
			mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
			next, captured := newCarrierHandler()

			req := carrierRequest(http.MethodGet, "/v1/agent/tasks", authorization, "Bearer smuggled-jwt")
			mw(next).ServeHTTP(httptest.NewRecorder(), req)

			if !captured.served {
				t.Fatal("request must pass through untouched")
			}
			if captured.authorization != authorization {
				t.Fatalf("authorization = %q, want it unchanged at %q", captured.authorization, authorization)
			}
			if captured.unwrapped {
				t.Fatal("a request without the shim key must never be marked unwrapped")
			}
			if captured.carrierSeen {
				t.Fatalf("%s must be stripped even when it is ignored", auth.UpstreamAuthHeader)
			}
		})
	}
}

// The middleware is a no-op with no shim key configured, but the carrier is
// still ours alone, so it is still taken off the request.
func TestOWUIUnwrap_HeaderCarrierStrippedWhenDisabled(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: ""})
	next, captured := newCarrierHandler()

	req := carrierRequest(http.MethodGet, "/v1/agent/tasks", "Bearer anything", "Bearer smuggled-jwt")
	mw(next).ServeHTTP(httptest.NewRecorder(), req)

	if captured.authorization != "Bearer anything" {
		t.Fatalf("disabled middleware must pass through, got %q", captured.authorization)
	}
	if captured.carrierSeen {
		t.Fatalf("%s must be stripped even when the middleware is disabled", auth.UpstreamAuthHeader)
	}
}

// The case that made the strip presence-gated rather than value-gated.
//
// Go's wire-level parser trims only ASCII space and tab (RFC 7230 OWS), while
// strings.TrimSpace treats U+00A0 as whitespace too, so a header carrying a
// non-breaking space arrives with a non-empty value that trims to "". A strip
// gated on the trimmed value would leave that header on the request and hand
// it to the handler, which is the one thing this header must never do.
func TestOWUIUnwrap_HeaderCarrierPresentButBlank_StrippedAndRejected(t *testing.T) {
	for name, value := range map[string]string{
		"spaces":             "   ",
		"tab":                "\t",
		"non-breaking space": " ",
		"bare scheme":        "Bearer ",
	} {
		t.Run(name, func(t *testing.T) {
			mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
			next, captured := newCarrierHandler()

			req := carrierRequest(http.MethodGet, "/v1/agent/tasks", "Bearer "+testShimKey, "")
			req.Header.Set(auth.UpstreamAuthHeader, value)

			rec := httptest.NewRecorder()
			mw(next).ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 for a carrier that carries nothing", rec.Code)
			}
			if captured.served {
				t.Fatal("a blank carrier must not reach the handler")
			}
			// The "Stripped" half of this test's name. Without it the test
			// asserts only "Rejected": `next` never runs on a 401, so the
			// capture handler observes nothing, and moving the strip out of
			// its unconditional position into the success branches alone
			// would leave every assertion above green while the rejection
			// paths stopped stripping. The middleware removes the header from
			// the inbound request itself, so the request this test still
			// holds is the one to inspect.
			if _, seen := req.Header[http.CanonicalHeaderKey(auth.UpstreamAuthHeader)]; seen {
				t.Fatal("the carrier must be stripped on the rejection path too")
			}
		})
	}
}

// The same shape on a request that is otherwise a plain pass-through: the
// header is not honoured, and it still does not survive.
func TestOWUIUnwrap_BlankHeaderCarrierStrippedOnPassThrough(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCarrierHandler()

	req := carrierRequest(http.MethodGet, "/v1/models", "Bearer hk_live_real_key", "")
	req.Header.Set(auth.UpstreamAuthHeader, " ")

	mw(next).ServeHTTP(httptest.NewRecorder(), req)

	if !captured.served {
		t.Fatal("a non-shim request must pass through")
	}
	if captured.carrierSeen {
		t.Fatal("a blank carrier must be stripped even when it is ignored")
	}
}

func TestOWUIUnwrap_HeaderCarrierOverLongToken_Rejects401(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCarrierHandler()

	req := carrierRequest(http.MethodGet, "/v1/agent/tasks", "Bearer "+testShimKey,
		"Bearer "+strings.Repeat("a", 9<<10))
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if captured.served {
		t.Fatal("an over-long carrier must not reach the handler")
	}
	// Same reasoning as the blank-carrier case: the 401 arms are exactly the
	// arms `next` cannot observe, so the strip is asserted on the request the
	// middleware was handed.
	if _, seen := req.Header[http.CanonicalHeaderKey(auth.UpstreamAuthHeader)]; seen {
		t.Fatal("an over-long carrier must still be stripped")
	}
}

// The fail-closed half. Without this the proxy losing its token would bill and
// audit every agent task against the shim's own principal instead of the
// signed-in user, silently, which is the exact failure owui_unwrap exists to
// prevent on the chat path.
func TestOWUIUnwrap_ShimKeyOnAgentPathWithoutCarrier_Rejects401(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"list", http.MethodGet, "/v1/agent/tasks"},
		{"get one", http.MethodGet, "/v1/agent/tasks/2f1c9d0e-0000-4000-8000-000000000000"},
		{"cancel", http.MethodPost, "/v1/agent/tasks/2f1c9d0e-0000-4000-8000-000000000000/cancel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
			next, captured := newCarrierHandler()

			req := carrierRequest(tc.method, tc.path, "Bearer "+testShimKey, "")
			rec := httptest.NewRecorder()
			mw(next).ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if captured.served {
				t.Fatal("a shim-key agent request with no per-user token must never reach the handler")
			}
		})
	}
}

func TestOWUIUnwrap_ShimKeyOnAgentCreateWithoutCarrier_Rejects401(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCarrierHandler()

	body, err := json.Marshal(map[string]any{"pack": "coding-pack", "instructions": "do a thing"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/tasks", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testShimKey)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if captured.served {
		t.Fatal("a shim-key create with no per-user token must never reach the handler")
	}
}

// The carrier is read before the body is, because a bodyless request is the
// case it exists for and reading the body first would 401 such a request
// before the carrier was ever consulted.
func TestOWUIUnwrap_HeaderCarrierWinsOverBodyMetadata(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCarrierHandler()

	body, err := json.Marshal(map[string]any{
		"__metadata": map[string]any{"upstream_auth": "Bearer body-jwt"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testShimKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.UpstreamAuthHeader, "Bearer header-jwt")

	mw(next).ServeHTTP(httptest.NewRecorder(), req)

	if captured.authorization != "Bearer header-jwt" {
		t.Fatalf("authorization = %q, want the header carrier to win", captured.authorization)
	}
}

// The header winning must not skip the body rewrite. __metadata is stripped
// unconditionally because forwarding proxy-layer fields would leak them to
// downstream handlers, into the audit chain and on to a provider, and a carrier
// that returned early would have quietly reintroduced exactly that.
func TestOWUIUnwrap_HeaderCarrierStillStripsMetadataFromTheBody(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})

	var forwarded []byte
	var authorization string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		forwarded, _ = io.ReadAll(r.Body)
		authorization = r.Header.Get("Authorization")
	})

	body, err := json.Marshal(map[string]any{
		"model": "hive-fast",
		"__metadata": map[string]any{
			"upstream_auth": "Bearer body-jwt",
			"chat_id":       "internal-only",
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testShimKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.UpstreamAuthHeader, "Bearer header-jwt")

	mw(next).ServeHTTP(httptest.NewRecorder(), req)

	if authorization != "Bearer header-jwt" {
		t.Fatalf("authorization = %q, want the header carrier to win", authorization)
	}
	if bytes.Contains(forwarded, []byte("__metadata")) {
		t.Fatalf("__metadata survived to the handler: %s", forwarded)
	}
	if bytes.Contains(forwarded, []byte("body-jwt")) {
		t.Fatalf("a token survived in the forwarded body: %s", forwarded)
	}
	if !bytes.Contains(forwarded, []byte("hive-fast")) {
		t.Fatalf("the rest of the body did not survive: %s", forwarded)
	}
}

// Paths where the shim key is itself the intended credential are unchanged.
func TestOWUIUnwrap_NonAgentBodylessShimRequestStillPassesThrough(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCarrierHandler()

	req := carrierRequest(http.MethodGet, "/v1/models", "Bearer "+testShimKey, "")
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if !captured.served {
		t.Fatalf("GET /v1/models must pass through with the shim key, got status %d", rec.Code)
	}
	if captured.authorization != "Bearer "+testShimKey {
		t.Fatalf("authorization = %q, want the shim key intact", captured.authorization)
	}
	if captured.unwrapped {
		t.Fatal("a pass-through must not be marked unwrapped")
	}
}

// Issue #1718. The two web tool routes spend real money: a search is charged
// 100,000 credits and a fetch 200,000, settled through sessionbilling against
// whichever principal edge-api resolves. Before this, they were not in
// requiresPerUserAuth, so a shim-key call arriving without a per-user token
// passed through under the shim account's own principal. That is the exact
// mis-attribution the agent-task arm was added to prevent, and here it would
// bill the shim account for every customer's web search while auditing none of
// them. Fail closed instead.
func TestOWUIUnwrap_ShimKeyOnWebToolCallWithoutCarrier_Rejects401(t *testing.T) {
	for _, path := range []string{"/v1/tools/web_search", "/v1/tools/web_fetch"} {
		t.Run(path, func(t *testing.T) {
			mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
			next, captured := newCarrierHandler()

			body, err := json.Marshal(map[string]any{"query": "who won", "url": "https://example.com"})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+testShimKey)
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			mw(next).ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if captured.served {
				t.Fatal("a shim-key web tool call with no per-user token must never reach the handler: it would be billed to the shim account")
			}
		})
	}
}

// The descriptor list is not a call and costs nothing, so it must stay
// reachable with the shim key alone. The chat shim reads it once per process
// to decide what to advertise, before any user's tool call exists.
func TestOWUIUnwrap_ShimKeyOnToolDescriptorListPassesThrough(t *testing.T) {
	mw := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	next, captured := newCarrierHandler()

	req := carrierRequest(http.MethodGet, "/v1/tools", "Bearer "+testShimKey, "")
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !captured.served {
		t.Fatal("GET /v1/tools under the shim key must reach the handler")
	}
}
