package webtools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// GET /v1/tools is the one place the chat shim reads the tool specifications
// from. Issue #1718: before it existed, Descriptors() had no non-test caller
// at all, so the only way for the front end to advertise these tools was a
// hardcoded copy that drifts from the handler implementing them.
func TestListRouteServesDescriptors(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(Deps{}).Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/tools", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/tools status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}

	var body ToolList
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if body.Object != "list" {
		t.Fatalf("object = %q, want list", body.Object)
	}
	names := map[string]bool{}
	for _, spec := range body.Data {
		names[spec.Function.Name] = true
		if spec.Type != "function" {
			t.Fatalf("%s: type = %q, want function", spec.Function.Name, spec.Type)
		}
		if spec.Function.Description == "" {
			t.Fatalf("%s: empty description", spec.Function.Name)
		}
	}
	if !names[ToolWebSearch] || !names[ToolWebFetch] {
		t.Fatalf("served names = %v, want both %s and %s", names, ToolWebSearch, ToolWebFetch)
	}

	// The endpoint must stay inside the same serialized budget the specs
	// themselves are held to, because what it serves is what ends up on every
	// chat request.
	if n := len(rec.Body.Bytes()); n > MaxDescriptorBytes+64 {
		t.Fatalf("served payload is %d bytes, over the %d budget", n, MaxDescriptorBytes)
	}
}

// The list route is a read of a compiled-in constant. It takes no body, spends
// no money and has no per-turn budget, so it must not answer a write verb.
func TestListRouteRefusesNonGET(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(Deps{}).Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/tools", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /v1/tools status = %d, want 405", rec.Code)
	}
}

// The list route must not shadow the two call routes. Registering "/v1/tools"
// on the same mux as "/v1/tools/web_search" is exactly the shape where a
// pattern change silently swallows a sibling.
func TestListRouteDoesNotShadowTheCallRoutes(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(Deps{}).Register(mux)

	for _, path := range []string{"/v1/tools/" + ToolWebSearch, "/v1/tools/" + ToolWebFetch} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		// No principal on the request, so the call routes reject it. What
		// matters is that they reject it as a call route (405 for the wrong
		// verb) rather than answering the descriptor list.
		if rec.Code == http.StatusOK {
			t.Fatalf("%s answered 200, so the list route is shadowing it", path)
		}
	}
}
