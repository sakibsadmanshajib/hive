package webtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/sessionbilling/billingtest"
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

// newTestHandler builds the two routes with a working money path unless the
// test supplies its own.
//
// The default is deliberate and is NOT a way to make billing optional: the
// handler refuses every call when Deps.Billing is absent (issue #1695), which
// TestWebToolRefusesWhenBillingIsNotWired holds in place by constructing the
// handler directly. Injecting a billable tenant here keeps every test about
// search behaviour, budgets and envelopes stating only what it is about,
// instead of restating the money path thirty times.
func newTestHandler(t *testing.T, d Deps) http.Handler {
	t.Helper()
	if d.Billing == nil {
		d.Billing = &Billing{
			Accounting: (&billingtest.Accounting{}).Client(t),
			Resolver:   billingtest.Billable(),
			Pricer:     catalogPricer(),
		}
	}
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
		{"oversized non-latin query", map[string]any{"query": strings.Repeat("খ", MaxQueryChars+1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := post(t, h, "/v1/tools/web_search", "turn-1", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body = %s", rr.Code, rr.Body)
			}
		})
	}
}

// The query limit counts characters, not bytes. Bangladesh is this product's
// first market and a Bangla character is three bytes, so a byte-counted limit
// would refuse a Bangla query a third the length of an English one.
func TestQueryLimitCountsCharactersNotBytes(t *testing.T) {
	search := &stubSearcher{hits: okHits()}
	h := newTestHandler(t, Deps{Search: search})
	// Well under MaxQueryChars characters, well over it in bytes.
	query := strings.Repeat("খ", MaxQueryChars-1)
	if len(query) <= MaxQueryChars {
		t.Fatalf("the fixture is %d bytes, it must exceed the %d byte figure to be meaningful",
			len(query), MaxQueryChars)
	}
	if rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": query}); rr.Code != http.StatusOK {
		t.Fatalf("a Bangla query of %d characters answered %d, want 200, body = %s",
			MaxQueryChars-1, rr.Code, rr.Body)
	}
}

// A tenant that churns turn identifiers must not be able to fill the shared
// budget map and deny every other tenant. Refusing past the cap would do
// exactly that, so the map evicts instead.
func TestBudgetMapCannotBeFilledToDenyAnotherTenant(t *testing.T) {
	b := &turnBudget{now: time.Now, turns: make(map[string]turnCounts), tenants: make(map[uuid.UUID]tenantCounts)}
	noisy := uuid.New()
	for i := 0; i < maxTrackedTurns*2; i++ {
		b.take(noisy, "turn-"+strconv.Itoa(i), ToolWebSearch, SearchBudgetPerTurn)
	}
	if len(b.turns) > maxTrackedTurns {
		t.Fatalf("the budget map grew to %d entries, over the %d cap", len(b.turns), maxTrackedTurns)
	}
	if v := b.take(uuid.New(), "victim-turn", ToolWebSearch, SearchBudgetPerTurn); v != budgetOK {
		t.Fatalf("an unrelated tenant got verdict %v after another tenant flooded the map", v)
	}
}

// A deployment with no search backend must not also burn the turn's
// allowance answering that it has none.
func TestUnconfiguredSearchDoesNotConsumeBudget(t *testing.T) {
	h := newTestHandler(t, Deps{})
	for i := 0; i < SearchBudgetPerTurn+2; i++ {
		rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "q"})
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("call %d answered %d, want 503, body = %s", i+1, rr.Code, rr.Body)
		}
		if env := decodeEnvelope(t, rr); env["code"] != CodeSearchUnavailable {
			t.Fatalf("call %d code = %v, want %q", i+1, env["code"], CodeSearchUnavailable)
		}
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

// The second half of spec section 7 item 10. The per-turn budget alone bounds
// nothing across turns, because the turn is a client-supplied header: a caller
// that increments it gets an unbounded call rate. This asserts the escape is
// closed, by doing exactly what an injected loop would do.
func TestTenantRateLimitBoundsTurnIdentifierChurn(t *testing.T) {
	search := &stubSearcher{hits: okHits()}
	h := newTestHandler(t, Deps{Search: search})

	allowed := 0
	var lastCode any
	for i := 0; i < TenantCallsPerMinute*3; i++ {
		// A fresh turn every call, which defeats the per-turn budget entirely.
		rr := post(t, h, "/v1/tools/web_search", "turn-"+strconv.Itoa(i), map[string]any{"query": "q"})
		if rr.Code == http.StatusOK {
			allowed++
			continue
		}
		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("call %d answered %d, want 200 or 429, body = %s", i+1, rr.Code, rr.Body)
		}
		lastCode = decodeEnvelope(t, rr)["code"]
	}
	if allowed != TenantCallsPerMinute {
		t.Fatalf("%d calls were served, want exactly %d", allowed, TenantCallsPerMinute)
	}
	if search.calls != TenantCallsPerMinute {
		t.Fatalf("the backend ran %d times, want %d", search.calls, TenantCallsPerMinute)
	}
	// The two refusals mean different things and must not collapse: this one
	// is "you are calling too fast", not "this turn is done".
	if lastCode != CodeRateLimited {
		t.Fatalf("code = %v, want %q", lastCode, CodeRateLimited)
	}
}

// The window is per tenant, so one tenant exhausting it must not refuse
// another. A shared counter here would be a cross-tenant denial of service.
func TestTenantRateLimitIsPerTenant(t *testing.T) {
	b := &turnBudget{now: time.Now, turns: make(map[string]turnCounts), tenants: make(map[uuid.UUID]tenantCounts)}
	noisy, quiet := uuid.New(), uuid.New()
	for i := 0; i < TenantCallsPerMinute*2; i++ {
		b.take(noisy, "turn-"+strconv.Itoa(i), ToolWebSearch, SearchBudgetPerTurn)
	}
	if v := b.take(quiet, "turn-1", ToolWebSearch, SearchBudgetPerTurn); v != budgetOK {
		t.Fatalf("an unrelated tenant got verdict %v after another tenant exhausted its window", v)
	}
}

// A refused per-turn call must not also spend the tenant's minute, or a turn
// that hits its own small budget would silently eat the larger allowance.
func TestTurnRefusalDoesNotSpendTheTenantWindow(t *testing.T) {
	b := &turnBudget{now: time.Now, turns: make(map[string]turnCounts), tenants: make(map[uuid.UUID]tenantCounts)}
	tenant := uuid.New()
	for i := 0; i < SearchBudgetPerTurn+5; i++ {
		b.take(tenant, "one-turn", ToolWebSearch, SearchBudgetPerTurn)
	}
	if got := b.tenants[tenant].calls; got != SearchBudgetPerTurn {
		t.Fatalf("the tenant window recorded %d calls, want %d: refused calls are being charged", got, SearchBudgetPerTurn)
	}
}

// Eviction under pressure must spend the flooder's own entries before a
// neighbour's. Deleting arbitrary keys is not a denial of service, but it does
// reset another tenant's injected-loop bound, which is the protection the
// budget exists to provide.
func TestEvictionSpendsTheFloodersOwnEntriesFirst(t *testing.T) {
	b := &turnBudget{now: time.Now, turns: make(map[string]turnCounts), tenants: make(map[uuid.UUID]tenantCounts)}
	victim, flooder := uuid.New(), uuid.New()

	// The victim spends its turn budget, so its counter is worth protecting.
	for i := 0; i < SearchBudgetPerTurn; i++ {
		b.take(victim, "victim-turn", ToolWebSearch, SearchBudgetPerTurn)
	}
	victimKey := victim.String() + "\x00" + "victim-turn"
	if _, ok := b.turns[victimKey]; !ok {
		t.Fatal("the victim's counter was not recorded")
	}

	// The flooder churns turn identifiers well past the cap. Its own tenant
	// window will refuse most of these, which is fine: the map pressure is
	// what this test is about, so drive it through evict directly.
	for i := 0; i < maxTrackedTurns*2; i++ {
		b.turns[flooder.String()+"\x00"+"flood-"+strconv.Itoa(i)] = turnCounts{expires: time.Now().Add(turnTTL)}
		if len(b.turns) >= maxTrackedTurns {
			b.evict(time.Now(), flooder.String()+"\x00")
		}
	}

	if _, ok := b.turns[victimKey]; !ok {
		t.Fatal("the victim's live counter was evicted while the flooder still held entries of its own")
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

// The invariant has to hold at the boundary the value crosses to the client,
// not only inside the constructor. FetchResult has exported fields, so a
// future pipeline can return one the constructor would have refused, and a
// nil error means errors.Is cannot catch it. Each row here is a shape that
// would otherwise reach the client as a 200 carrying no sources, which is
// precisely the #1609 defect reached around the constructor rather than
// through it.
func TestWebFetchRefusesANonConformingEnvelopeFromThePipeline(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result FetchResult
	}{
		{"zero value, the forgot-to-set-an-error shape", FetchResult{}},
		{"ok status with no parts", FetchResult{Status: StatusOK, URL: "https://example.com/a"}},
		{"ok status with an empty parts slice", FetchResult{Status: StatusOK, Parts: []Part{}}},
		{"parts but no status", FetchResult{Parts: []Part{{Text: "body", End: 4}}}},
		{"error status leaking through the success path", FetchResult{Status: StatusError, Parts: []Part{{Text: "x", End: 1}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler(t, Deps{
				Search: &stubSearcher{},
				Fetch: FetcherFunc(func(context.Context, string, string) (FetchResult, error) {
					return tc.result, nil
				}),
			})
			rr := post(t, h, "/v1/tools/web_fetch", "turn-1", map[string]any{"url": "https://example.com/a"})
			if rr.Code == http.StatusOK {
				t.Fatalf("a non-conforming envelope was served as a success: %s", rr.Body)
			}
			env := decodeEnvelope(t, rr)
			if env["status"] != StatusError {
				t.Fatalf("status = %v, want %q", env["status"], StatusError)
			}
			if _, present := env["parts"]; present {
				t.Fatalf("the refusal carries a parts field: %s", rr.Body)
			}
		})
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
	rr := postRaw(t, h, "/v1/tools/web_search", "turn-1",
		`{"query":"`+strings.Repeat("a", MaxToolRequestBytes+64)+`"}`)
	if rr.Code != http.StatusRequestEntityTooLarge && rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 413 or 400", rr.Code)
	}
}

// json.Decoder stops at the first complete value, so a small valid object can
// hide an arbitrary amount of trailing data behind it: without a read to EOF
// neither the trailing bytes nor the size cap is ever noticed. Both shapes
// are refused, and the search backend is never reached.
func TestWebToolsRejectTrailingDataAfterTheJSONBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"trailing json value", `{"query":"who won"}{"query":"and again"}`, http.StatusBadRequest},
		{"trailing garbage", `{"query":"who won"} not json at all`, http.StatusBadRequest},
		// Trailing bytes that are themselves valid JSON are the case that
		// actually reaches the size cap: the decoder keeps reading until
		// MaxBytesReader stops it. Unparseable trailing bytes, as above, fail
		// as a syntax error in the first buffered chunk instead, which is a
		// different status but the same refusal.
		{
			"oversized valid payload hidden behind a small valid value",
			`{"query":"who won"}"` + strings.Repeat("a", MaxToolRequestBytes+64) + `"`,
			http.StatusRequestEntityTooLarge,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			search := &stubSearcher{hits: okHits()}
			h := newTestHandler(t, Deps{Search: search})
			rr := postRaw(t, h, "/v1/tools/web_search", "turn-1", tc.body)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d, body = %s", rr.Code, tc.want, rr.Body)
			}
			if search.calls != 0 {
				t.Fatalf("the search backend ran %d times on a malformed body", search.calls)
			}
		})
	}
}

// Trailing whitespace is not trailing data. A client that ends its body with a
// newline is well behaved and must not be refused.
func TestWebToolsAcceptTrailingWhitespace(t *testing.T) {
	h := newTestHandler(t, Deps{Search: &stubSearcher{hits: okHits()}})
	rr := postRaw(t, h, "/v1/tools/web_search", "turn-1", "{\"query\":\"who won\"}\n\n  ")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rr.Code, rr.Body)
	}
}

func postRaw(t *testing.T, h http.Handler, path, turn, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if turn != "" {
		req.Header.Set(TurnHeader, turn)
	}
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: uuid.New(), TenantID: uuid.New()}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
