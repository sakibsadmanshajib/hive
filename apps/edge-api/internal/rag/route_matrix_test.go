package rag

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/matrix"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/middleware"
)

// realMatrixPath is the committed support matrix cmd/server/main.go loads at
// runtime and the edge-api image bakes in. The whole value of this file is that
// it reads that file rather than a fixture: a fixture would have been updated
// alongside a new route and proved nothing.
const realMatrixPath = "../../../../packages/openai-contract/matrix/support-matrix.json"

// ragEndpoints is the leaf-level truth about what /v1/rag/* serves: one entry
// per method and path template the package dispatches to a real handler.
// Handler.Register mounts four mux patterns, two of them subtrees, and the
// method plus suffix switches inside routeDocumentByID and routeProjectByID are
// invisible from a mux pattern, so this list is the only place the leaves are
// written down.
//
// Keep it in step with Register. A route that is registered but missing here,
// or here but missing from support-matrix.json, is a route that answers 404 on
// a real server: main.go wraps everything in
// middleware.UnsupportedEndpointMiddleware, which rejects any /v1/ path whose
// method and path pair the matrix does not carry. The boot-time
// assertMatrixCoverage guard cannot see this, and says so in its own doc
// comment: it reads mux patterns, so the covered "/v1/rag/" subtree satisfies
// it while every new leaf under that prefix stays uncovered. That is exactly
// how the six Projects routes shipped inert with four green authorization
// tests over them, because those tests drove the bare Handler through httptest
// and never touched the middleware chain.
var ragEndpoints = []struct{ method, path string }{
	{http.MethodPost, "/v1/rag/documents"},
	{http.MethodGet, "/v1/rag/documents"},
	{http.MethodGet, "/v1/rag/documents/{document_id}"},
	{http.MethodDelete, "/v1/rag/documents/{document_id}"},
	{http.MethodPost, "/v1/rag/search"},
	{http.MethodPost, "/v1/rag/chat"},
	{http.MethodPost, "/v1/rag/projects"},
	{http.MethodGet, "/v1/rag/projects"},
	{http.MethodGet, "/v1/rag/projects/{project_id}"},
	{http.MethodPatch, "/v1/rag/projects/{project_id}"},
	{http.MethodDelete, "/v1/rag/projects/{project_id}"},
	{http.MethodPost, "/v1/rag/projects/{project_id}/documents"},
}

// concreteID replaces every {template} segment with a real UUID, so the
// requests below also exercise matrix.pathMatchesTemplate rather than only the
// exact-key map lookup.
func concreteID(path string) string {
	const id = "11111111-1111-1111-1111-111111111111"
	out := make([]string, 0, 8)
	for _, seg := range strings.Split(path, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			seg = id
		}
		out = append(out, seg)
	}
	return strings.Join(out, "/")
}

// TestRAGRoutesReachHandlersThroughTheMiddlewareChain drives every /v1/rag/*
// route through the assembled chain (real support matrix, real
// UnsupportedEndpointMiddleware, real Handler.Register mux) instead of through
// the bare handler, which is the one thing every other test in this package
// does not do.
//
// An unauthenticated request is the cheapest probe that tells the two answers
// apart: a route that exists answers 401 from the handler's own auth check, and
// a route the matrix does not carry answers 404 from the middleware without the
// handler ever running.
func TestRAGRoutesReachHandlersThroughTheMiddlewareChain(t *testing.T) {
	m, err := matrix.LoadMatrix(realMatrixPath)
	if err != nil {
		t.Fatalf("loading support matrix from %s: %v", realMatrixPath, err)
	}

	mux := http.NewServeMux()
	// A handler with no store, no embedder and no audit sink is enough: every
	// route below refuses an unauthenticated caller before it touches any of
	// them, and that refusal is exactly the signal this test reads.
	NewHandler(nil, nil, nil, nil, context.Background()).Register(mux)
	handler := middleware.UnsupportedEndpointMiddleware(m)(mux)

	for _, ep := range ragEndpoints {
		path := concreteID(ep.path)
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(ep.method, path, nil))

			if rec.Code == http.StatusNotFound {
				t.Fatalf("%s %s answered 404 on the assembled chain: "+
					"packages/openai-contract/matrix/support-matrix.json carries no supported_now "+
					"entry for it, so UnsupportedEndpointMiddleware rejects it before the handler runs",
					ep.method, path)
			}
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s reached the chain but did not answer the handler's own "+
					"unauthenticated refusal: status=%d body=%q", ep.method, path, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestRAGMatrixEntriesAreAllServed is the reverse direction: a matrix entry
// under /v1/rag/ that no route serves is a documented endpoint that does not
// exist, which misleads a client author in the opposite way. It also catches a
// typo in an entry added alongside a route, where the route is real, the entry
// is real, and the two do not match.
func TestRAGMatrixEntriesAreAllServed(t *testing.T) {
	m, err := matrix.LoadMatrix(realMatrixPath)
	if err != nil {
		t.Fatalf("loading support matrix from %s: %v", realMatrixPath, err)
	}

	served := make(map[string]bool, len(ragEndpoints))
	for _, ep := range ragEndpoints {
		served[ep.method+" "+ep.path] = true
	}
	for _, entry := range m.Endpoints {
		if !strings.HasPrefix(entry.Path, "/v1/rag/") {
			continue
		}
		key := entry.Method + " " + entry.Path
		if !served[key] {
			t.Errorf("support-matrix.json declares %s supported_now but no route in this package serves it", key)
		}
	}
}
