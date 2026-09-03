// Package webtools implements the two Hive-owned web tools the chat surface
// advertises to models: web_search, which takes a query string and returns
// ranked citable hits, and web_fetch, which takes a URL and returns usable
// content parts.
//
// The boundary between them is the whole point. web_search never fetches a
// page, embeds anything or calls a model; web_fetch never runs a search. That
// split is what turns one user action from five page fetches plus roughly two
// hundred embedding calls (the shape that produced issue #1609) into one HTTP
// call the model can act on, deciding for itself whether any URL is worth the
// cost of fetching.
//
// The invariant that gives the package its reason to exist is in this file: a
// success envelope has exactly one constructor per tool, and both refuse to
// build one over an empty slice. "Dropped every source but reported success"
// is not a representable state here, so it cannot be reached by forgetting a
// branch. A backend that answered with nothing is the distinct StatusEmpty
// value, and a backend that failed is an error envelope naming the class.
//
// Design: spec-2026-09-01-web-search-and-web-fetch-tools.md, slice S1.
package webtools

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Envelope status values. StatusOK and StatusEmpty are both outcomes of a
// call that reached its backend, and they are deliberately different values:
// "the backend answered with nothing" and "we lost every candidate" must
// never render the same way, which is exactly the collapse #1609 shipped.
const (
	StatusOK    = "ok"
	StatusEmpty = "empty"
	StatusError = "error"
)

// Tool names, as advertised to the model, as registered by the Open WebUI shim
// and as spelled in the two route paths.
//
// A note that was here claimed these are upstream's own builtin names, so that
// the fork's citation extraction would keep working unchanged. They are not:
// the pinned v0.10.2 image calls its builtins `search_web` and `fetch_url`, and
// its extraction (utils/middleware.py get_citation_source_from_tool_result)
// dispatches on those literals, so a result under these names produced no
// citation at all. Rather than rename these, which would also rename the
// routes, the shim patch normalises the two names onto upstream's inside that
// one function (deploy/docker/owui-patches/apply_web_tools_patch.py, issue
// #1718). The intent the old note described is now true rather than assumed.
const (
	ToolWebSearch = "web_search"
	ToolWebFetch  = "web_fetch"
)

// Error codes. One per failure class, spelled once here so the two tools and
// the shim that renders them cannot drift. The pipeline stages that emit the
// fetch-side codes below arrive with slice S2; the vocabulary is fixed here
// because the envelope shape is this file's job.
const (
	CodeInvalidRequest         = "invalid_request"
	CodeBudgetExhausted        = "budget_exhausted"
	CodeRateLimited            = "rate_limited"
	CodeNotImplemented         = "not_implemented"
	CodeSearchUnavailable      = "search_unavailable"
	CodeURLRejected            = "url_rejected"
	CodeFetchTimeout           = "fetch_timeout"
	CodeFetchTooLarge          = "fetch_too_large"
	CodeFetchStatus            = "fetch_status"
	CodeFetchBlockedRedirect   = "fetch_blocked_redirect"
	CodeUnsupportedContentType = "unsupported_content_type"
	CodeExtractFailed          = "extract_failed"
	CodeExtractEmpty           = "extract_empty"
	CodeEmbedUnavailable       = "embed_unavailable"
	CodeReduceEmpty            = "reduce_empty"
	CodeFetchFailed            = "fetch_failed"
	// The money-path classes (issue #1695). Three, not one: "add credits",
	// "this workspace was never set up for usage" and "the accounting seam is
	// down" are different facts with different actions behind them, and the
	// model is told which one happened rather than a single opaque failure.
	CodeInsufficientCredit   = "insufficient_credit"
	CodeBillingNotConfigured = "billing_not_configured"
	CodeBillingUnavailable   = "billing_unavailable"
)

// ErrEmptyResult is what both success constructors return when handed nothing
// to report. Callers turn it into StatusEmpty or into an error envelope; what
// they cannot do is turn it into a success.
var ErrEmptyResult = errors.New("webtools: refusing to build a success envelope over zero items")

// Hit is one ranked, citable search result. Every field except Rank comes
// from the search backend; Rank is assigned positionally by NewSearchResult
// so it always describes the order the model is actually shown.
type Hit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Rank    int    `json:"rank"`
}

// SearchResult is the web_search success envelope. Dropped counts candidate
// results the backend returned that could not be carried (missing field,
// inadmissible URL), so a partial loss reads as a partial loss rather than
// being rounded to success or to failure.
type SearchResult struct {
	Status  string `json:"status"`
	Query   string `json:"query"`
	Results []Hit  `json:"results"`
	Dropped int    `json:"dropped"`
}

// NewSearchResult is the only constructor of a web_search success envelope.
// It refuses an empty slice, and it refuses any hit missing a title, an
// absolute http(s) URL or a snippet, so acceptance criterion A1 is enforced
// where no caller can forget it rather than at each call site.
func NewSearchResult(query string, hits []Hit, dropped int) (SearchResult, error) {
	if len(hits) == 0 {
		return SearchResult{}, ErrEmptyResult
	}
	ranked := make([]Hit, len(hits))
	for i, h := range hits {
		if strings.TrimSpace(h.Title) == "" {
			return SearchResult{}, fmt.Errorf("webtools: hit %d has no title", i)
		}
		if strings.TrimSpace(h.Snippet) == "" {
			return SearchResult{}, fmt.Errorf("webtools: hit %d has no snippet", i)
		}
		parsed, err := url.Parse(h.URL)
		if err != nil || !parsed.IsAbs() || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return SearchResult{}, fmt.Errorf("webtools: hit %d has no absolute http(s) URL", i)
		}
		h.Rank = i + 1
		ranked[i] = h
	}
	return SearchResult{Status: StatusOK, Query: query, Results: ranked, Dropped: dropped}, nil
}

// EmptySearchResult is the envelope for a backend that answered with zero
// usable results. It is a success in the sense that nothing failed, and it is
// not StatusOK, so the shim can render "no results" differently from "here
// are your results" without inspecting the length of a list.
func EmptySearchResult(query string, dropped int) SearchResult {
	return SearchResult{Status: StatusEmpty, Query: query, Results: []Hit{}, Dropped: dropped}
}

// Part is one extracted span of a fetched page, carrying the offsets it
// occupied in the extracted document so the model can say where in the page a
// quote came from.
//
// Start and End are counted in characters, not bytes, and the pipeline that
// fills them (slice S2) must count them the same way. Every "chars" figure on
// this envelope is a rune count: a byte count would report a Bangla page as
// three times its real length, and Bangladesh is this product's first market.
type Part struct {
	Text  string `json:"text"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// FetchMeta is everything a fetch envelope reports about the page itself,
// separated from the parts so NewFetchResult keeps one obvious argument for
// the thing the invariant is about.
type FetchMeta struct {
	URL        string
	FinalURL   string
	Title      string
	Truncated  bool
	TotalChars int
	Dropped    int
}

// FetchResult is the web_fetch success envelope.
type FetchResult struct {
	Status         string `json:"status"`
	URL            string `json:"url"`
	FinalURL       string `json:"final_url"`
	Title          string `json:"title"`
	Parts          []Part `json:"parts"`
	Truncated      bool   `json:"truncated"`
	TotalChars     int    `json:"total_chars"`
	RetrievedChars int    `json:"retrieved_chars"`
	Dropped        int    `json:"dropped"`
}

// NewFetchResult is the only constructor of a web_fetch success envelope, and
// it refuses an empty slice for the same reason NewSearchResult does
// (criterion B1). A page that produced no parts is an extract_empty or
// reduce_empty error, never a success with nothing in it.
func NewFetchResult(meta FetchMeta, parts []Part) (FetchResult, error) {
	if len(parts) == 0 {
		return FetchResult{}, ErrEmptyResult
	}
	// Characters, not bytes, matching Part.Start and Part.End. len() here
	// would report three times the real figure for Bangla or any other
	// non-Latin script.
	retrieved := 0
	for _, p := range parts {
		retrieved += utf8.RuneCountInString(p.Text)
	}
	final := meta.FinalURL
	if final == "" {
		final = meta.URL
	}
	return FetchResult{
		Status:         StatusOK,
		URL:            meta.URL,
		FinalURL:       final,
		Title:          meta.Title,
		Parts:          parts,
		Truncated:      meta.Truncated,
		TotalChars:     meta.TotalChars,
		RetrievedChars: retrieved,
		Dropped:        meta.Dropped,
	}, nil
}

// ErrorEnvelope is what either tool returns when it cannot return content. It
// carries a machine-readable class and a fixed human message; it never
// carries a results or parts field, so it cannot be read as an empty success.
type ErrorEnvelope struct {
	Status  string `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Dropped int    `json:"dropped"`
}

// NewError builds an error envelope. Messages passed here are fixed strings
// chosen by the handler, never a wrapped internal error: criterion B11 says
// no message either tool emits may name an internal service or a resolved
// address, and the cheapest way to hold that is to never interpolate one.
func NewError(code, message string, dropped int) ErrorEnvelope {
	return ErrorEnvelope{Status: StatusError, Code: code, Message: message, Dropped: dropped}
}
