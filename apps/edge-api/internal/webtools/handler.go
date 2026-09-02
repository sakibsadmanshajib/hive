package webtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
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
	// MaxFetchQueryChars caps the path and query of a fetch URL taken
	// together, which are the two carriers an injected page would use to send
	// conversation content out. Well below MaxURLChars, and comfortably above
	// what an article URL with tracking parameters carries. Bytes, since a URL
	// is percent-encoded ASCII on the wire.
	MaxFetchQueryChars = 512
	// MaxFocusChars caps the focus string web_fetch ranks against, in runes.
	MaxFocusChars = 512
	// maxTurnIDChars bounds the turn identifier so the budget map's keys
	// cannot be grown by a caller.
	maxTurnIDChars = 128

	// SearchBudgetPerTurn and FetchBudgetPerTurn are the first half of spec
	// section 7 item 10. Without them an injected page can walk the model
	// through an unbounded tool loop, and since a URL carries a query string,
	// an unbounded fetch loop is an exfiltration channel rather than merely a
	// cost.
	SearchBudgetPerTurn = 2
	FetchBudgetPerTurn  = 3

	// TenantCallsPerMinute is the second half of that item, and it is the half
	// that actually bounds a tenant. The per-turn budget alone bounds nothing
	// across turns: the turn is a client-supplied header, so any holder of a
	// session JWT, including the shim itself if an injected page walks it
	// there, gets an unbounded call rate by incrementing the header. Two per
	// turn times unlimited turns is unlimited.
	//
	// The number is deliberately generous against real use (a turn spends at
	// most 2 searches and 3 fetches, so this is roughly six full turns a
	// minute) and ruinous against a loop.
	//
	// Live consequence if this is absent: unbounded SearXNG queries from this
	// box's single egress IP, which is the same surface that forced the engine
	// list down to three in issue #1576 and PR #1585. Armed consequence: once
	// the fetch pipeline lands, the same loop is an unbounded outbound relay.
	TenantCallsPerMinute = 30
	// tenantWindow is the window TenantCallsPerMinute is counted over.
	tenantWindow = time.Minute

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
	msgRateLimited     = "Too many web tool calls in the last minute. Try again shortly."
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
	// The money-path messages (issue #1695). None names an amount, a balance,
	// a provider or an internal service.
	msgInsufficientCredit   = "Your available credit does not cover this web tool call. Add credits and try again."
	msgBillingNotConfigured = "This workspace is not set up for usage yet. Contact your administrator to complete workspace setup."
	msgBillingUnavailable   = "This tool call could not be billed, so it was not run. Please retry."
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
	// Billing is the money path both tools settle through (issue #1695). It is
	// REQUIRED: a handler built without it refuses every call rather than
	// serving provider spend for free, which is what both tools did before it
	// existed. See billing.go for the rules it enforces.
	Billing *Billing
	// Now is the clock the per-turn budget uses. Nil means time.Now.
	Now func() time.Time
}

// Handler serves the two tool routes.
type Handler struct {
	search  Searcher
	fetch   Fetcher
	billing *Billing
	budget  *turnBudget
}

// NewHandler builds the handler.
func NewHandler(d Deps) *Handler {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Handler{
		search:  d.Search,
		fetch:   d.Fetch,
		billing: d.Billing,
		budget:  &turnBudget{now: now, turns: make(map[string]turnCounts), tenants: make(map[uuid.UUID]tenantCounts)},
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

	if verdict := h.budget.take(user.TenantID, turn, ToolWebSearch, SearchBudgetPerTurn); verdict != budgetOK {
		writeBudgetRefusal(w, verdict)
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

	// The hold, taken before the backend is called and after every refusal
	// that costs nothing (issue #1695). A call this deployment was never going
	// to serve creates no reservation.
	charge, ok := h.beginCharge(r.Context(), w, user, ToolWebSearch)
	if !ok {
		return
	}
	// Settled exactly once, after the response has been written. Charged unless
	// an exit path below refuses; see toolCharge.settle for why that is the
	// safe default here.
	defer charge.settle()

	hits, dropped, err := h.search.Search(r.Context(), query, count)
	if err != nil {
		// Logged with the real cause, answered without it. Not charged: an
		// errored search is not a delivered one.
		charge.refuse("search_failed")
		log.Printf("webtools: web_search failed: %v", err)
		writeEnvelope(w, http.StatusBadGateway, NewError(CodeSearchUnavailable, msgSearchDown, dropped))
		return
	}
	if len(hits) == 0 {
		// Charged, by falling through to the deferred settle. The query reached
		// SearXNG and consumed exactly what SearXNG costs, which is one query;
		// results are what it returns, not what it bills for. This is a
		// delivered call that found nothing, not a failure.
		writeEnvelope(w, http.StatusOK, EmptySearchResult(query, dropped))
		return
	}

	envelope, err := NewSearchResult(query, hits, dropped)
	if err != nil {
		// The constructor refused what the backend produced. That is a bug
		// in the backend adapter, and it is reported as a failure rather
		// than papered over with a shorter list.
		charge.refuse("envelope_refused")
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
	// The exfiltration channel, narrowed. An injected page can plausibly
	// instruct the model to fetch an attacker-chosen URL, and a URL carries
	// conversation content out in whatever part of itself the attacker picks,
	// so the channel is real and this slice does not close it. What it does is
	// bound it: without this the bound was MaxURLChars, 2048 bytes per URL
	// times three fetches a turn, about 6 KB of conversation per turn and 61 KB
	// a minute per tenant. This cuts the per-URL carrier to 512 bytes.
	//
	// Path AND query together, not the query alone. A first version capped
	// only RawQuery, which is the obvious carrier and not the only one:
	// https://attacker.example/<1900 bytes of base64> has an empty RawQuery,
	// so it passed that check and the 2048 byte check above it, and the whole
	// URL still carried what the cap claimed to have removed. A stated bound
	// that is not the bound is the defect this package has now had three of.
	//
	// Still not closure. A payload in a subdomain reaches the attacker's
	// authoritative nameserver through resolution alone, and no length check
	// here touches that. Bounding the two carriers a fetch actually returns
	// through is blast-radius reduction, which is what the spec asks for.
	//
	// Deliberately here rather than in Admit. Admit also screens web_search
	// hits, and a search result with a long tracking query is a legitimate
	// result rather than an exfiltration attempt; refusing it there would drop
	// hits for a reason that does not apply to them. The channel is the fetch
	// tool, so the cap is on the fetch route.
	if len(admitted.Path)+len(admitted.RawQuery) > MaxFetchQueryChars {
		writeEnvelope(w, http.StatusBadRequest, NewError(CodeURLRejected, msgURLRejected, 0))
		return
	}

	if verdict := h.budget.take(user.TenantID, turn, ToolWebFetch, FetchBudgetPerTurn); verdict != budgetOK {
		writeBudgetRefusal(w, verdict)
		return
	}

	if h.fetch == nil {
		writeEnvelope(w, http.StatusNotImplemented, NewError(CodeNotImplemented, msgFetchNotWired, 0))
		return
	}

	// The hold, taken after the 501 above so a deployment with no pipeline
	// wired does not reserve credits for a call it cannot make.
	charge, ok := h.beginCharge(r.Context(), w, user, ToolWebFetch)
	if !ok {
		return
	}
	defer charge.settle()

	result, err := h.fetch.Fetch(r.Context(), admitted.String(), focus)
	if err != nil {
		// Logged with the real cause, answered without it. Not charged: a
		// page that could not be read is not a delivered fetch.
		charge.refuse("fetch_failed")
		log.Printf("webtools: web_fetch failed: %v", err)
		code := fetchCode(err)
		writeEnvelope(w, fetchStatus(code), NewError(code, fetchMessage(code, err), 0))
		return
	}
	// The invariant is enforced here, at the boundary the value crosses to
	// the client, and not only in NewFetchResult.
	//
	// FetchResult has exported fields, so the constructor is not the only
	// route to one. A pipeline that returns FetchResult{} with a nil error,
	// which is what a path that forgot to set an error returns, would
	// otherwise be written verbatim as a 200 carrying {"status":"","parts":
	// null}. That is the #1609 shape reached around the constructor rather
	// than through it, and errors.Is on a nil error cannot catch it. The
	// pipeline is written in a later slice by someone who will reasonably
	// assume the envelope cannot lie, so the check belongs on this side.
	if result.Status != StatusOK || len(result.Parts) == 0 {
		charge.refuse("nonconforming_envelope")
		log.Printf("webtools: web_fetch pipeline returned a non-conforming envelope (status=%q parts=%d)",
			result.Status, len(result.Parts))
		writeEnvelope(w, http.StatusBadGateway, NewError(CodeExtractEmpty, msgFetchFailed, result.Dropped))
		return
	}
	writeEnvelope(w, http.StatusOK, result)
}

// fetchCode maps a pipeline error to the envelope class. Criterion B9's whole
// point is that these classes never collapse into one: a refused URL, a slow
// page, a page that answered 404, a PDF this deployment cannot read and a
// page that rendered nothing are five different facts, and a model told the
// wrong one answers wrongly.
func fetchCode(err error) string {
	switch {
	// Before ErrURLRejected, deliberately. A blocked redirect wraps BOTH
	// sentinels, because CheckRedirect re-admits the hop through Admit and
	// keeps its reason: errors.Is is true for either, and whichever arm is
	// written first wins. Asked in the other order this returns url_rejected
	// for a redirect, which tells the model the URL it was given was refused
	// when the URL it was given was fine and the page tried to move it
	// somewhere private. That is exactly the collapse criterion B9 forbids,
	// and it is a live defect until this ordering is here.
	case errors.Is(err, ErrBlockedRedirect), errors.Is(err, ErrTooManyRedirects):
		return CodeFetchBlockedRedirect
	case errors.Is(err, ErrURLRejected):
		return CodeURLRejected
	case errors.Is(err, ErrBlockedAddress), errors.Is(err, ErrResolveFailed):
		return CodeURLRejected
	case errors.Is(err, ErrDialFailed):
		// The address was admissible and simply would not answer. That is a
		// failed fetch, not a refused URL, and collapsing the two would tell
		// the model the address was forbidden when it was not.
		return CodeFetchFailed
	case errors.Is(err, context.DeadlineExceeded):
		return CodeFetchTimeout
	case errors.Is(err, ErrTooLarge):
		return CodeFetchTooLarge
	case errors.Is(err, ErrFetchStatus):
		return CodeFetchStatus
	case errors.Is(err, ErrUnsupportedContentType):
		return CodeUnsupportedContentType
	case errors.Is(err, ErrExtractEmpty), errors.Is(err, ErrEmptyResult):
		return CodeExtractEmpty
	case errors.Is(err, ErrExtractFailed):
		return CodeExtractFailed
	case errors.Is(err, ErrEmbedUnavailable):
		return CodeEmbedUnavailable
	case errors.Is(err, ErrReduceEmpty):
		return CodeReduceEmpty
	default:
		return CodeFetchFailed
	}
}

// fetchMessage is what the model and the user are told about a failed fetch.
//
// One message per class, because spec section 6 is explicit that a timeout
// and a block are never collapsed into one message: "the page is slow" and
// "the page tried to send us somewhere private" are different facts. Two
// classes carry one machine fact each, an HTTP status and a media type, and
// both are shape-checked at the point they are built. Everything else is a
// fixed string, which is how criterion B11 is held permanently: an internal
// error is never interpolated into a message, so it can never leak one.
func fetchMessage(code string, err error) string {
	switch code {
	case CodeURLRejected:
		return msgURLRejected
	case CodeFetchTimeout:
		return "That page took too long to answer, so nothing was read from it."
	case CodeFetchTooLarge:
		return "That page is too large to read."
	case CodeFetchBlockedRedirect:
		return "That page redirected to an address that cannot be fetched, so it was not followed."
	case CodeFetchStatus:
		if status := upstreamStatusOf(err); status != 0 {
			return fmt.Sprintf("That page answered with HTTP status %d, so it has no content to read.", status)
		}
		return "That page answered with an error status, so it has no content to read."
	case CodeUnsupportedContentType:
		if mediaType := mediaTypeOf(err); mediaType != "" {
			return fmt.Sprintf("That link is %s, which this tool cannot read.", mediaType)
		}
		return "That link is not a page or a document this tool can read."
	case CodeExtractFailed:
		return "That page loaded, but its content could not be read."
	case CodeExtractEmpty:
		// Said explicitly, because this is the case most likely to be misread
		// as the model failing rather than the extraction failing.
		return "That page loaded successfully but has no readable text."
	case CodeEmbedUnavailable:
		return "That page loaded, but its content could not be processed right now."
	case CodeReduceEmpty:
		return "That page loaded, but no part of it could be selected as relevant."
	default:
		return msgFetchFailed
	}
}

// fetchStatus is the HTTP status the envelope is served with. The envelope's
// own code is what the shim reads; this is for anything in between that reads
// only the status line.
func fetchStatus(code string) int {
	switch code {
	case CodeURLRejected, CodeFetchBlockedRedirect:
		return http.StatusBadRequest
	case CodeFetchTimeout:
		return http.StatusGatewayTimeout
	case CodeFetchTooLarge:
		return http.StatusRequestEntityTooLarge
	case CodeUnsupportedContentType:
		return http.StatusUnsupportedMediaType
	default:
		return http.StatusBadGateway
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

// writeBudgetRefusal answers a refused call. The two limits get different
// codes because they mean different things to the caller: one turn has spent
// its allowance and the next turn will work, versus this tenant is calling
// too fast and needs to wait.
func writeBudgetRefusal(w http.ResponseWriter, verdict budgetVerdict) {
	if verdict == budgetTenantRateLimited {
		w.Header().Set("Retry-After", strconv.Itoa(int(tenantWindow.Seconds())))
		writeEnvelope(w, http.StatusTooManyRequests, NewError(CodeRateLimited, msgRateLimited, 0))
		return
	}
	writeEnvelope(w, http.StatusTooManyRequests, NewError(CodeBudgetExhausted, msgBudget, 0))
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

// budgetVerdict distinguishes the two ways a call can be refused, so the
// envelope names which one happened rather than collapsing them.
type budgetVerdict int

const (
	budgetOK budgetVerdict = iota
	budgetTurnExhausted
	budgetTenantRateLimited
)

// tenantCounts is one tenant's calls in the current fixed window.
type tenantCounts struct {
	calls   int
	expires time.Time
}

// turnBudget bounds tool calls two ways: per assistant turn, and per tenant
// per minute. Both halves are needed and neither substitutes for the other.
// The turn budget stops an injected page looping within one turn, where the
// caller fixes the identifier. The tenant window stops that same loop escaping
// the bound by incrementing the identifier, which any client can do freely
// because the turn arrives in a header.
//
// ponytail: two in-memory maps under one mutex, per process. Two ceilings,
// both deliberate. Budgets are not shared across edge-api replicas, so N
// replicas allow N times the limit; that is fine while edge-api runs
// single-replica on the demo box, and the upgrade path is the Redis this
// process already talks to for the budget gate. And the tenant window is
// fixed rather than sliding, so a caller can spend two windows' worth across
// a boundary; that costs one extra window, not an unbounded rate, which is
// the property this exists for.
type turnBudget struct {
	mu      sync.Mutex
	now     func() time.Time
	turns   map[string]turnCounts
	tenants map[uuid.UUID]tenantCounts
}

// take consumes one call of the given tool and reports whether it was allowed.
// The per-turn budget is checked first, so a turn that has already spent its
// allowance does not also spend the tenant's minute.
func (b *turnBudget) take(tenant uuid.UUID, turn, tool string, limit int) budgetVerdict {
	prefix := tenant.String() + "\x00"
	key := prefix + turn

	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()

	entry, known := b.turns[key]
	if !known || now.After(entry.expires) {
		if len(b.turns) >= maxTrackedTurns {
			b.evict(now, prefix)
		}
		entry = turnCounts{expires: now.Add(turnTTL)}
	}

	switch tool {
	case ToolWebSearch:
		if entry.searches >= limit {
			return budgetTurnExhausted
		}
	default:
		if entry.fetches >= limit {
			return budgetTurnExhausted
		}
	}

	if !b.takeTenant(tenant, now) {
		return budgetTenantRateLimited
	}

	if tool == ToolWebSearch {
		entry.searches++
	} else {
		entry.fetches++
	}
	b.turns[key] = entry
	return budgetOK
}

// takeTenant consumes one call against the tenant's fixed window. Called under
// b.mu.
func (b *turnBudget) takeTenant(tenant uuid.UUID, now time.Time) bool {
	if b.tenants == nil {
		b.tenants = make(map[uuid.UUID]tenantCounts)
	}
	window, known := b.tenants[tenant]
	if !known || now.After(window.expires) {
		// Keys here are authenticated tenant ids, not client-supplied
		// strings, so a caller cannot grow this map the way it can grow the
		// turn map. Sweeping expired entries only keeps it from accumulating
		// every tenant the process has ever served.
		if len(b.tenants) >= maxTrackedTurns {
			for id, w := range b.tenants {
				if now.After(w.expires) {
					delete(b.tenants, id)
				}
			}
		}
		b.tenants[tenant] = tenantCounts{calls: 1, expires: now.Add(tenantWindow)}
		return true
	}
	if window.calls >= TenantCallsPerMinute {
		return false
	}
	window.calls++
	b.tenants[tenant] = window
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
// The residual, and why the caller's own prefix is passed in: map iteration
// order gives no preference to anybody, so evicting arbitrary keys let a
// tenant that floods the map delete other tenants' live counters as a side
// effect. That is not a denial of service, but it does reset a neighbour's
// injected-loop bound, which is the one protection the budget provides. The
// flooder now pays for its own flood first and only spills onto neighbours
// once its own entries are exhausted, which needs it to hold the whole map.
//
// Called under b.mu.
func (b *turnBudget) evict(now time.Time, tenantPrefix string) {
	for key, entry := range b.turns {
		if now.After(entry.expires) {
			delete(b.turns, key)
		}
	}
	for _, own := range []bool{true, false} {
		for key := range b.turns {
			if len(b.turns) < maxTrackedTurns {
				return
			}
			if strings.HasPrefix(key, tenantPrefix) == own {
				delete(b.turns, key)
			}
		}
	}
}
