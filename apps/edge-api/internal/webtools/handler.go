package webtools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// HTTP surface for the two tools: POST /v1/tools/web_search and POST
// /v1/tools/web_fetch, both authenticated as JWT-session routes exactly like
// /v1/rag/*. Because edge-api resolves the JWT principal here, anything
// web_fetch later spends on embeddings charges the tenant that ran the fetch,
// rather than the shared shim account today's document RAG charges.
//
// These endpoints are not advertised on the API-key surface. That would
// change every existing customer's payload and their bill, and it is
// explicitly out of scope (spec section 11 question 3).

const (
	// TurnHeader carries the assistant turn the tool call belongs to. The
	// Open WebUI shim sends the assistant message id.
	TurnHeader = "X-Hive-Tool-Turn"

	// MaxToolRequestBytes caps a tool request body. Both bodies are a query
	// or a URL and a short focus string; nothing legitimate is near this.
	MaxToolRequestBytes = 16 << 10

	// MaxQueryChars caps a search query, and MaxURLChars a fetch URL. Query
	// and focus are counted in runes rather than bytes: Bangladesh is this
	// product's first market, a Bangla character is three bytes, and a byte
	// count would silently refuse a query a third the length of an English
	// one. A URL is percent-encoded ASCII on the wire, so bytes are the right
	// unit there.
	MaxQueryChars = 512
	MaxURLChars   = 2048
	// MaxFocusChars caps the focus string web_fetch ranks against, in runes.
	MaxFocusChars = 512
	// maxTurnIDChars bounds the turn identifier so the budget map's keys
	// cannot be grown by a caller.
	maxTurnIDChars = 128

	// SearchBudgetPerTurn and FetchBudgetPerTurn are spec section 7 item 10.
	// Without them an injected page can walk the model through an unbounded
	// tool loop, and since a URL carries a query string, an unbounded fetch
	// loop is an exfiltration channel rather than merely a cost.
	SearchBudgetPerTurn = 2
	FetchBudgetPerTurn  = 3

	// turnTTL is how long a turn's counters are remembered. Longer than any
	// assistant turn, short enough that the map does not accumulate.
	turnTTL = 15 * time.Minute
	// maxTrackedTurns bounds the budget map. Past it, entries are evicted
	// rather than new turns refused; see evict for why that direction is the
	// safe one when the map is shared across tenants.
	maxTrackedTurns = 4096
)

// Fixed client-facing messages. Criterion B11 requires that no message either
// tool emits names an internal service or a resolved address, and the way to
// hold that permanently is to never interpolate an internal error into one.
const (
	msgNoTurn          = "This tool call carries no turn identifier, so its per-turn budget cannot be applied."
	msgBudget          = "This turn has already used its allowance of web tool calls."
	msgBadBody         = "The tool arguments could not be read."
	msgNoQuery         = "A non-empty query string is required."
	msgQueryTooLong    = "The query is too long."
	msgNoURL           = "An absolute http(s) URL is required."
	msgURLRejected     = "That address cannot be fetched."
	msgSearchDown      = "Web search is unavailable right now."
	msgFetchNotWired   = "Fetching page content is not available on this deployment yet."
	msgFetchFailed     = "The page could not be processed."
	msgBodyTooLarge    = "The tool arguments are too large."
	msgMethodNotAllwed = "Method not allowed."
)

// Fetcher is the web_fetch pipeline. It is nil in this slice: the pipeline is
// slice S2, and until it lands the route answers an explicit 501
// not_implemented rather than anything that could be mistaken for a page that
// happened to have no content.
type Fetcher interface {
	Fetch(ctx context.Context, target, focus string) (FetchResult, error)
}

// FetcherFunc adapts a function to Fetcher.
type FetcherFunc func(ctx context.Context, target, focus string) (FetchResult, error)

// Fetch implements Fetcher.
func (f FetcherFunc) Fetch(ctx context.Context, target, focus string) (FetchResult, error) {
	return f(ctx, target, focus)
}

// Deps are the handler's dependencies.
type Deps struct {
	// Search is the web_search backend. A nil Search makes the search route
	// answer search_unavailable, never an empty result set.
	Search Searcher
	// Fetch is the web_fetch pipeline, nil until slice S2 wires it.
	Fetch Fetcher
	// Now is the clock the per-turn budget uses. Nil means time.Now.
	Now func() time.Time
}

// Handler serves the two tool routes.
type Handler struct {
	search Searcher
	fetch  Fetcher
	budget *turnBudget
}

// NewHandler builds the handler.
func NewHandler(d Deps) *Handler {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Handler{
		search: d.Search,
		fetch:  d.Fetch,
		budget: &turnBudget{now: now, turns: make(map[string]turnCounts)},
	}
}

// Register attaches both routes. Both are registered in this slice even
// though only one is implemented, so the route table is touched once by one
// change rather than twice.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/tools/"+ToolWebSearch, h.handleSearch)
	mux.HandleFunc("/v1/tools/"+ToolWebFetch, h.handleFetch)
}

type searchRequest struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

type fetchRequest struct {
	URL   string `json:"url"`
	Focus string `json:"focus"`
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	user, turn, ok := h.admitCall(w, r)
	if !ok {
		return
	}

	var req searchRequest
	if !decodeBody(w, r, &req) {
		return
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		writeEnvelope(w, http.StatusBadRequest, NewError(CodeInvalidRequest, msgNoQuery, 0))
		return
	}
	if utf8.RuneCountInString(query) > MaxQueryChars {
		writeEnvelope(w, http.StatusBadRequest, NewError(CodeInvalidRequest, msgQueryTooLong, 0))
		return
	}

	// Checked before the budget is consumed: a deployment with no search
	// backend wired should not also burn the turn's allowance answering that
	// it has none.
	if h.search == nil {
		writeEnvelope(w, http.StatusServiceUnavailable, NewError(CodeSearchUnavailable, msgSearchDown, 0))
		return
	}

	if !h.budget.take(user.TenantID, turn, ToolWebSearch, SearchBudgetPerTurn) {
		writeEnvelope(w, http.StatusTooManyRequests, NewError(CodeBudgetExhausted, msgBudget, 0))
		return
	}

	// Clamped here as well as inside the client. max_results arrives in the
	// request body, so it is bounded at the trust boundary rather than only
	// at the place that happens to consume it.
	count := req.MaxResults
	if count <= 0 {
		count = DefaultMaxResults
	}
	if count > MaxResultsCeiling {
		count = MaxResultsCeiling
	}

	hits, dropped, err := h.search.Search(r.Context(), query, count)
	if err != nil {
		// Logged with the real cause, answered without it.
		log.Printf("webtools: web_search failed: %v", err)
		writeEnvelope(w, http.StatusBadGateway, NewError(CodeSearchUnavailable, msgSearchDown, dropped))
		return
	}
	if len(hits) == 0 {
		writeEnvelope(w, http.StatusOK, EmptySearchResult(query, dropped))
		return
	}

	envelope, err := NewSearchResult(query, hits, dropped)
	if err != nil {
		// The constructor refused what the backend produced. That is a bug
		// in the backend adapter, and it is reported as a failure rather
		// than papered over with a shorter list.
		log.Printf("webtools: web_search envelope refused: %v", err)
		writeEnvelope(w, http.StatusBadGateway, NewError(CodeSearchUnavailable, msgSearchDown, dropped+len(hits)))
		return
	}
	writeEnvelope(w, http.StatusOK, envelope)
}

func (h *Handler) handleFetch(w http.ResponseWriter, r *http.Request) {
	user, turn, ok := h.admitCall(w, r)
	if !ok {
		return
	}

	var req fetchRequest
	if !decodeBody(w, r, &req) {
		return
	}
	raw := strings.TrimSpace(req.URL)
	if raw == "" || len(raw) > MaxURLChars {
		writeEnvelope(w, http.StatusBadRequest, NewError(CodeInvalidRequest, msgNoURL, 0))
		return
	}
	focus := truncateRunes(strings.TrimSpace(req.Focus), MaxFocusChars)

	// Stage 0 runs before anything else, so a refused URL is refused
	// identically whether or not the pipeline behind it exists.
	admitted, err := Admit(raw)
	if err != nil {
		writeEnvelope(w, http.StatusBadRequest, NewError(CodeURLRejected, msgURLRejected, 0))
		return
	}

	if !h.budget.take(user.TenantID, turn, ToolWebFetch, FetchBudgetPerTurn) {
		writeEnvelope(w, http.StatusTooManyRequests, NewError(CodeBudgetExhausted, msgBudget, 0))
		return
	}

	if h.fetch == nil {
		writeEnvelope(w, http.StatusNotImplemented, NewError(CodeNotImplemented, msgFetchNotWired, 0))
		return
	}

	result, err := h.fetch.Fetch(r.Context(), admitted.String(), focus)
	if err != nil {
		log.Printf("webtools: web_fetch failed: %v", err)
		writeEnvelope(w, http.StatusBadGateway, NewError(fetchCode(err), msgFetchFailed, 0))
		return
	}
	writeEnvelope(w, http.StatusOK, result)
}

// fetchCode maps a pipeline error to the envelope class. The pipeline itself
// is slice S2; what this slice fixes is that the classes never collapse into
// one, which is criterion B9's whole point.
func fetchCode(err error) string {
	switch {
	case errors.Is(err, ErrURLRejected):
		return CodeURLRejected
	case errors.Is(err, ErrBlockedRedirect), errors.Is(err, ErrTooManyRedirects):
		return CodeFetchBlockedRedirect
	case errors.Is(err, ErrBlockedAddress), errors.Is(err, ErrResolveFailed):
		return CodeURLRejected
	case errors.Is(err, context.DeadlineExceeded):
		return CodeFetchTimeout
	case errors.Is(err, ErrEmptyResult):
		return CodeExtractEmpty
	default:
		return CodeFetchFailed
	}
}

// admitCall applies everything both routes share: method, session, turn id.
// It reports false when it has already written a response.
func (h *Handler) admitCall(w http.ResponseWriter, r *http.Request) (*auth.User, string, bool) {
	if r.Method != http.MethodPost {
		writeEnvelope(w, http.StatusMethodNotAllowed, NewError(CodeInvalidRequest, msgMethodNotAllwed, 0))
		return nil, "", false
	}
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return nil, "", false
	}
	turn := strings.TrimSpace(r.Header.Get(TurnHeader))
	// No turn identifier means the per-turn budget cannot be applied, and a
	// budget that fails open is the defect it exists to prevent. Refuse.
	if turn == "" || len(turn) > maxTurnIDChars {
		writeEnvelope(w, http.StatusBadRequest, NewError(CodeInvalidRequest, msgNoTurn, 0))
		return nil, "", false
	}
	return user, turn, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, into any) bool {
	defer r.Body.Close()
	// Deliberately lenient about unknown fields: the caller is the Open WebUI
	// shim, and a field it adds later must not turn every tool call into a
	// parse failure whose message says nothing useful.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxToolRequestBytes))
	if err := dec.Decode(into); err != nil {
		writeBodyError(w, err)
		return false
	}
	// Decode stops at the first complete JSON value and never looks at what
	// follows, so on its own it neither rejects trailing data nor reads far
	// enough for MaxBytesReader to notice an oversized body behind a small
	// valid one. Reading on until EOF settles both: trailing bytes are a bad
	// body, and trailing bytes past the cap are an oversized one.
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		if err == nil {
			writeEnvelope(w, http.StatusBadRequest, NewError(CodeInvalidRequest, msgBadBody, 0))
			return false
		}
		writeBodyError(w, err)
		return false
	}
	return true
}

func writeBodyError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeEnvelope(w, http.StatusRequestEntityTooLarge, NewError(CodeInvalidRequest, msgBodyTooLarge, 0))
		return
	}
	writeEnvelope(w, http.StatusBadRequest, NewError(CodeInvalidRequest, msgBadBody, 0))
}

func writeEnvelope(w http.ResponseWriter, status int, envelope any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(envelope); err != nil {
		log.Printf("webtools: writing response: %v", err)
	}
}

// turnCounts is one assistant turn's consumption.
type turnCounts struct {
	searches int
	fetches  int
	expires  time.Time
}

// turnBudget bounds tool calls per assistant turn.
//
// ponytail: one in-memory map, per process. The ceiling is that budgets are
// not shared across edge-api replicas, so N replicas allow up to N times the
// budget for one turn. That is acceptable while edge-api runs single-replica
// on the demo box and the box is the only deployment; the upgrade path if it
// ever runs multi-replica is the same Redis this process already talks to for
// the budget gate.
type turnBudget struct {
	mu    sync.Mutex
	now   func() time.Time
	turns map[string]turnCounts
}

// take consumes one call of the given tool against the turn's budget and
// reports whether it was within it.
func (b *turnBudget) take(tenant uuid.UUID, turn, tool string, limit int) bool {
	key := tenant.String() + "\x00" + turn

	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()

	entry, known := b.turns[key]
	if !known || now.After(entry.expires) {
		if len(b.turns) >= maxTrackedTurns {
			b.evict(now)
		}
		entry = turnCounts{expires: now.Add(turnTTL)}
	}

	switch tool {
	case ToolWebSearch:
		if entry.searches >= limit {
			return false
		}
		entry.searches++
	default:
		if entry.fetches >= limit {
			return false
		}
		entry.fetches++
	}
	b.turns[key] = entry
	return true
}

// evict makes room in a full map: expired turns first, then, if that was not
// enough, arbitrary live ones until the map is back under the cap.
//
// Evicting rather than refusing is deliberate. Refusing a new turn once the
// map is full would let one tenant deny every other tenant's web access by
// sending maxTrackedTurns distinct turn identifiers, since the map is shared
// across tenants. Evicting cannot do that. What it costs is that a tenant who
// churns turn identifiers may reset a budget early, and that is not a bypass
// worth defending: a fresh turn identifier already earns a fresh budget by
// design. The budget exists to stop an injected page walking the model
// through a loop within one assistant turn, where the identifier is fixed by
// the caller, not to ration a tenant across turns.
//
// Called under b.mu.
func (b *turnBudget) evict(now time.Time) {
	for key, entry := range b.turns {
		if now.After(entry.expires) {
			delete(b.turns, key)
		}
	}
	for key := range b.turns {
		if len(b.turns) < maxTrackedTurns {
			return
		}
		delete(b.turns, key)
	}
}
