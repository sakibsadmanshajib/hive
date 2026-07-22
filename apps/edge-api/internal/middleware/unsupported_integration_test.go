package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/matrix"
)

// realMatrixPath points at the committed support matrix that cmd/server/main.go
// loads at runtime and that the edge-api Docker image bakes in. Exercising the
// real file (not a hand-built fixture) is the whole point of this test: the
// Waves 2-4 agent-subsystem routes (/v1/rag, /v1/agent, /v1/artifacts) shipped
// with their handlers wired but without matrix entries, so every request 404-ed
// at UnsupportedEndpointMiddleware. The per-package isolation tests bypassed
// this middleware entirely, which is why the break reached a demo build green.
const realMatrixPath = "../../../../packages/openai-contract/matrix/support-matrix.json"

// TestUnsupportedEndpointMiddleware_AgentSubsystemRoutesReachHandlers drives the
// proprietary agent-subsystem paths through the real middleware + real matrix.
// Before the matrix carried supported_now entries for these paths every case
// returned 404; after, each case must reach the downstream handler.
func TestUnsupportedEndpointMiddleware_AgentSubsystemRoutesReachHandlers(t *testing.T) {
	// Defaults to the real committed matrix so this stays a true CI regression
	// guard. HIVE_MATRIX_PATH_FOR_TEST only exists so the fix's before/after
	// evidence can rerun the identical test body against the pre-change matrix.
	matrixPath := realMatrixPath
	if override := os.Getenv("HIVE_MATRIX_PATH_FOR_TEST"); override != "" {
		matrixPath = override
	}

	m, err := matrix.LoadMatrix(matrixPath)
	if err != nil {
		t.Fatalf("loading support matrix from %s: %v", matrixPath, err)
	}

	const reached = "REACHED"
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(reached))
	})
	handler := UnsupportedEndpointMiddleware(m)(next)

	// A concrete UUID stands in for every {id} template segment so the test
	// also exercises pathMatchesTemplate, not just exact-key map lookups.
	const id = "11111111-1111-1111-1111-111111111111"

	cases := []struct {
		method string
		path   string
	}{
		// RAG (FeatureRAG gate) — internal/rag/handler.go + chat_handler.go.
		{http.MethodPost, "/v1/rag/documents"},
		{http.MethodGet, "/v1/rag/documents"},
		{http.MethodGet, "/v1/rag/documents/" + id},
		{http.MethodDelete, "/v1/rag/documents/" + id},
		{http.MethodPost, "/v1/rag/search"},
		{http.MethodPost, "/v1/rag/chat"},
		// Agent tasks (FeatureCowork gate) — internal/agenttask/handler.go.
		{http.MethodPost, "/v1/agent/tasks"},
		{http.MethodGet, "/v1/agent/tasks"},
		{http.MethodGet, "/v1/agent/tasks/" + id},
		{http.MethodPost, "/v1/agent/tasks/" + id + "/cancel"},
		// Artifacts management (JWT selector) — internal/artifacts/handler.go.
		// Only the real POST routes are asserted; the anonymous serving routes
		// live under /artifacts/ (no /v1/ prefix) and never hit this middleware.
		{http.MethodPost, "/v1/artifacts"},
		{http.MethodPost, "/v1/artifacts/" + id + "/versions"},
		{http.MethodPost, "/v1/artifacts/" + id + "/share"},
		// Feature-gate read (issue #293) — internal/featuregate/handler.go.
		// Consumed by agent-console's isCoworkEnabled() and OWUI's gate
		// Function; shipped registered on the mux (main.go) but missing from
		// the matrix, so it 404-ed exactly like the routes above once did.
		{http.MethodGet, "/v1/featuregate"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusNotFound {
				t.Fatalf("%s %s was rejected by UnsupportedEndpointMiddleware (404); "+
					"support-matrix.json is missing a supported_now entry for it",
					tc.method, tc.path)
			}
			if rec.Code != http.StatusOK || rec.Body.String() != reached {
				t.Fatalf("%s %s did not reach the downstream handler: status=%d body=%q",
					tc.method, tc.path, rec.Code, rec.Body.String())
			}
		})
	}
}
