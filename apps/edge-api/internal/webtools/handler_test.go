package webtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

type stubSearcher struct {
	hits    []Hit
	dropped int
	err     error
	calls   int
}

func (s *stubSearcher) Search(_ context.Context, _ string, n int) ([]Hit, int, error) {
	s.calls++
	if s.err != nil {
		return nil, s.dropped, s.err
	}
	// Mirror the real client: a caller that asks for no particular count gets
	// the default, not zero results.
	if n <= 0 {
		n = DefaultMaxResults
	}
	if n < len(s.hits) {
		return s.hits[:n], s.dropped, nil
	}
	return s.hits, s.dropped, nil
}

func okHits() []Hit {
	return []Hit{
		{Title: "First", URL: "https://example.com/1", Snippet: "one"},
		{Title: "Second", URL: "https://example.com/2", Snippet: "two"},
	}
}

func newTestHandler(t *testing.T, d Deps) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	NewHandler(d).Register(mux)
	return mux
}

func post(t *testing.T, h http.Handler, path, turn string, body any) *httptest.ResponseRecorder {
	t.Helper()
	blob, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(blob)))
	if turn != "" {
		req.Header.Set(TurnHeader, turn)
	}
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID: uuid.New(), TenantID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decodeEnvelope(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding %q: %v", rr.Body.String(), err)
	}
	return out
}

func TestWebSearchReturnsHits(t *testing.T) {
	h := newTestHandler(t, Deps{Search: &stubSearcher{hits: okHits()}})
	rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "who won"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	env := decodeEnvelope(t, rr)
	if env["status"] != StatusOK {
		t.Fatalf("status = %v, want %q", env["status"], StatusOK)
	}
	results, _ := env["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

// A3 through the route: zero hits is status "empty", never a success with an
// empty list and never an error.
func TestWebSearchZeroResultsIsEmptyNotOK(t *testing.T) {
	h := newTestHandler(t, Deps{Search: &stubSearcher{}})
	rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "asdkjhasdkjh"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	env := decodeEnvelope(t, rr)
	if env["status"] != StatusEmpty {
		t.Fatalf("status = %v, want %q", env["status"], StatusEmpty)
	}
	if env["query"] != "asdkjhasdkjh" {
		t.Fatalf("query = %v, want the query echoed back", env["query"])
	}
}

// A4 through the route: an unavailable backend is an error envelope with a
// named class, and the HTTP status is not 200 either, so a broken SearXNG is
// visible to monitoring and not only to the model.
func TestWebSearchBackendFailureIsAnErrorEnvelope(t *testing.T) {
	h := newTestHandler(t, Deps{Search: &stubSearcher{err: fmt.Errorf("%w: status 429", ErrSearchBackend)}})
	rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "who won"})
	if rr.Code == http.StatusOK {
		t.Fatalf("a failed search answered 200: %s", rr.Body)
	}
	env := decodeEnvelope(t, rr)
	if env["status"] != StatusError {
		t.Fatalf("status = %v, want %q", env["status"], StatusError)
	}
	if env["code"] != CodeSearchUnavailable {
		t.Fatalf("code = %v, want %q", env["code"], CodeSearchUnavailable)
	}
	if _, present := env["results"]; present {
		t.Fatal("an error envelope carries a results field; it must not be mistakable for an empty success")
	}
}

func TestWebSearchRejectsBadRequests(t *testing.T) {
	h := newTestHandler(t, Deps{Search: &stubSearcher{hits: okHits()}})
	for _, tc := range []struct {
		name string
		body any
	}{
		{"no query", map[string]any{}},
		{"blank query", map[string]any{"query": "   "}},
		{"oversized query", map[string]any{"query": strings.Repeat("a", MaxQueryChars+1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := post(t, h, "/v1/tools/web_search", "turn-1", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body = %s", rr.Code, rr.Body)
			}
		})
	}
}

func TestWebToolsRequireAnAuthenticatedSession(t *testing.T) {
	h := newTestHandler(t, Deps{Search: &stubSearcher{hits: okHits()}})
	for _, path := range []string{"/v1/tools/web_search", "/v1/tools/web_fetch"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"query":"x","url":"https://example.com/"}`))
		req.Header.Set(TurnHeader, "turn-1")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", path, rr.Code)
		}
	}
}

func TestWebToolsRejectNonPOST(t *testing.T) {
	h := newTestHandler(t, Deps{Search: &stubSearcher{hits: okHits()}})
	for _, path := range []string{"/v1/tools/web_search", "/v1/tools/web_fetch"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: uuid.New(), TenantID: uuid.New()}))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want 405", path, rr.Code)
		}
	}
}

// The per-turn budget cannot be enforced without a turn identifier, so a call
// that carries none is refused rather than served unbudgeted. The shim sends
// the assistant message id.
func TestWebToolsRequireATurnIdentifier(t *testing.T) {
	search := &stubSearcher{hits: okHits()}
	h := newTestHandler(t, Deps{Search: search})
	rr := post(t, h, "/v1/tools/web_search", "", map[string]any{"query": "who won"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rr.Code, rr.Body)
	}
	if search.calls != 0 {
		t.Fatalf("the search backend was called %d times without a turn id", search.calls)
	}
}

// Spec section 7 item 10: at most 2 web_search calls per assistant turn.
// Without a budget an injected page can walk the model through an unbounded
// tool loop.
func TestWebSearchBudgetIsPerTurn(t *testing.T) {
	search := &stubSearcher{hits: okHits()}
	h := newTestHandler(t, Deps{Search: search})

	for i := 0; i < SearchBudgetPerTurn; i++ {
		if rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "q"}); rr.Code != http.StatusOK {
			t.Fatalf("call %d status = %d, body = %s", i+1, rr.Code, rr.Body)
		}
	}
	rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "q"})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("the over-budget call answered %d, want 429, body = %s", rr.Code, rr.Body)
	}
	if env := decodeEnvelope(t, rr); env["code"] != CodeBudgetExhausted {
		t.Fatalf("code = %v, want %q", env["code"], CodeBudgetExhausted)
	}
	if search.calls != SearchBudgetPerTurn {
		t.Fatalf("the backend was called %d times, want %d", search.calls, SearchBudgetPerTurn)
	}

	// A new turn starts with a fresh budget, otherwise a long conversation
	// would run out of web access partway through.
	if rr := post(t, h, "/v1/tools/web_search", "turn-2", map[string]any{"query": "q"}); rr.Code != http.StatusOK {
		t.Fatalf("the first call of a new turn answered %d, want 200", rr.Code)
	}
}

// B12. The fourth web_fetch call within one assistant turn is refused. The
// budget is taken before the pipeline runs, so this holds whether or not the
// pipeline behind it exists.
func TestWebFetchBudgetIsPerTurn(t *testing.T) {
	var fetches int
	h := newTestHandler(t, Deps{
		Search: &stubSearcher{},
		Fetch: FetcherFunc(func(_ context.Context, target, _ string) (FetchResult, error) {
			fetches++
			return NewFetchResult(FetchMeta{URL: target}, []Part{{Text: "body", End: 4}})
		}),
	})
	body := map[string]any{"url": "https://example.com/a"}

	for i := 0; i < FetchBudgetPerTurn; i++ {
		if rr := post(t, h, "/v1/tools/web_fetch", "turn-1", body); rr.Code != http.StatusOK {
			t.Fatalf("call %d status = %d, body = %s", i+1, rr.Code, rr.Body)
		}
	}
	rr := post(t, h, "/v1/tools/web_fetch", "turn-1", body)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("call %d answered %d, want 429, body = %s", FetchBudgetPerTurn+1, rr.Code, rr.Body)
	}
	if env := decodeEnvelope(t, rr); env["code"] != CodeBudgetExhausted {
		t.Fatalf("code = %v, want %q", env["code"], CodeBudgetExhausted)
	}
	if fetches != FetchBudgetPerTurn {
		t.Fatalf("the pipeline ran %d times, want %d", fetches, FetchBudgetPerTurn)
	}
}

// The two tools hold separate allowances within a turn: exhausting searches
// must not silently consume the fetch budget.
func TestSearchAndFetchBudgetsAreIndependent(t *testing.T) {
	h := newTestHandler(t, Deps{
		Search: &stubSearcher{hits: okHits()},
		Fetch: FetcherFunc(func(_ context.Context, target, _ string) (FetchResult, error) {
			return NewFetchResult(FetchMeta{URL: target}, []Part{{Text: "body", End: 4}})
		}),
	})
	for i := 0; i < SearchBudgetPerTurn; i++ {
		post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "q"})
	}
	rr := post(t, h, "/v1/tools/web_fetch", "turn-1", map[string]any{"url": "https://example.com/a"})
	if rr.Code != http.StatusOK {
		t.Fatalf("a fetch after an exhausted search budget answered %d, want 200, body = %s", rr.Code, rr.Body)
	}
}

// B2 through the route. Admission runs before anything else the fetch path
// would do, so a refused URL is refused identically whether or not the fetch
// pipeline exists yet.
func TestWebFetchRefusesInadmissibleURLs(t *testing.T) {
	h := newTestHandler(t, Deps{Search: &stubSearcher{}})
	for _, raw := range []string{
		"http://127.0.0.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://metadata.google.internal/",
		"http://100.100.100.200/",
		"http://[fd00:ec2::254]/",
		"http://control-plane:8081/",
		"file:///etc/passwd",
		"http://10.0.0.5/",
	} {
		rr := post(t, h, "/v1/tools/web_fetch", "turn-"+raw, map[string]any{"url": raw})
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400, body = %s", raw, rr.Code, rr.Body)
		}
		env := decodeEnvelope(t, rr)
		if env["code"] != CodeURLRejected {
			t.Fatalf("%s: code = %v, want %q", raw, env["code"], CodeURLRejected)
		}
	}
}

// The route exists in this slice; its body pipeline is S2. Until that lands
// the endpoint says so loudly, with an error envelope and a 501, so nothing
// can mistake the gap for a page that had no content.
func TestWebFetchIsExplicitlyNotImplementedWithoutAPipeline(t *testing.T) {
	h := newTestHandler(t, Deps{Search: &stubSearcher{}})
	rr := post(t, h, "/v1/tools/web_fetch", "turn-1", map[string]any{"url": "https://example.com/a"})
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501, body = %s", rr.Code, rr.Body)
	}
	env := decodeEnvelope(t, rr)
	if env["status"] != StatusError {
		t.Fatalf("status = %v, want %q", env["status"], StatusError)
	}
	if env["code"] != CodeNotImplemented {
		t.Fatalf("code = %v, want %q", env["code"], CodeNotImplemented)
	}
	if _, present := env["parts"]; present {
		t.Fatal("the not-implemented envelope carries a parts field")
	}
}

// With a pipeline wired (S2) the handler hands the admitted URL straight to
// it. Asserted here so the seam S2 fills is covered by S1's own suite.
func TestWebFetchDelegatesToTheFetcherWhenWired(t *testing.T) {
	var got string
	h := newTestHandler(t, Deps{
		Search: &stubSearcher{},
		Fetch: FetcherFunc(func(_ context.Context, target, _ string) (FetchResult, error) {
			got = target
			return NewFetchResult(FetchMeta{URL: target, FinalURL: target, Title: "T"},
				[]Part{{Text: "body", End: 4}})
		}),
	})
	rr := post(t, h, "/v1/tools/web_fetch", "turn-1", map[string]any{"url": "https://example.com/a", "focus": "f"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	if got != "https://example.com/a" {
		t.Fatalf("the fetcher received %q", got)
	}
}

// B11. No message either tool emits names an internal service or an address.
func TestNoEnvelopeLeaksInternalTopology(t *testing.T) {
	leaks := []string{"edge-api", "litellm", "control-plane", "searxng", "markitdown", "127.0.0.1", "169.254.169.254"}
	cases := []struct {
		path string
		turn string
		body any
		deps Deps
	}{
		{"/v1/tools/web_search", "t1", map[string]any{"query": "q"},
			Deps{Search: &stubSearcher{err: fmt.Errorf("dial tcp searxng:8080 connect to 172.19.0.4: refused: %w", ErrSearchBackend)}}},
		{"/v1/tools/web_search", "t2", map[string]any{},
			Deps{Search: &stubSearcher{}}},
		{"/v1/tools/web_fetch", "t3", map[string]any{"url": "http://control-plane:8081/x"},
			Deps{Search: &stubSearcher{}}},
		{"/v1/tools/web_fetch", "t4", map[string]any{"url": "http://169.254.169.254/"},
			Deps{Search: &stubSearcher{}}},
		{"/v1/tools/web_fetch", "t5", map[string]any{"url": "https://example.com/a"},
			Deps{Search: &stubSearcher{}, Fetch: FetcherFunc(func(context.Context, string, string) (FetchResult, error) {
				return FetchResult{}, errors.New("markitdown at 172.19.0.9 refused the document")
			})}},
	}
	for _, tc := range cases {
		h := newTestHandler(t, tc.deps)
		rr := post(t, h, tc.path, tc.turn, tc.body)
		body := rr.Body.String()
		for _, leak := range leaks {
			if strings.Contains(body, leak) {
				t.Fatalf("%s response leaks %q: %s", tc.path, leak, body)
			}
		}
	}
}

// A body larger than the small cap these two endpoints need is refused
// outright rather than buffered.
func TestWebToolsRefuseOversizedBodies(t *testing.T) {
	h := newTestHandler(t, Deps{Search: &stubSearcher{hits: okHits()}})
	req := httptest.NewRequest(http.MethodPost, "/v1/tools/web_search",
		strings.NewReader(`{"query":"`+strings.Repeat("a", MaxToolRequestBytes+64)+`"}`))
	req.Header.Set(TurnHeader, "turn-1")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: uuid.New(), TenantID: uuid.New()}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge && rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 413 or 400", rr.Code)
	}
}
