package webtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// The SearXNG client behind web_search. One HTTP call per search, and that is
// the entire tool: no page is fetched, nothing is embedded, no model is asked
// to write the query. The model writes the query itself as the tool argument,
// which is what removes the separate query-generation task turn (#1600's
// defect surface) from the search path.
//
// The engine set is not configured here. deploy/searxng/settings.yml pins it
// to the three engines measured to actually answer from this box's egress IP
// (bing, wikipedia, stackoverflow; issue #1576, PR #1585), and widening it is
// a separate quality question, not a client-side parameter.

const (
	// DefaultSearchTimeout bounds one SearXNG call. SearXNG aggregates
	// several engines and is itself the slow part, so this is generous
	// relative to how little the call does.
	DefaultSearchTimeout = 12 * time.Second
	// DefaultMaxResults is how many hits a search returns when the caller
	// does not ask for a specific number.
	DefaultMaxResults = 5
	// MaxResultsCeiling caps what a caller can ask for. More than this is
	// context spent rather than context used: the model decides from
	// snippets, and a long snippet list crowds out the answer.
	MaxResultsCeiling = 10
	// maxSnippetChars trims an over-long backend snippet so one verbose
	// result cannot dominate the tool output. Counted in runes, not bytes:
	// Bangladesh is this product's first market and a Bangla snippet is three
	// bytes per character, so a byte count would cut a legible snippet to a
	// third of its length and could split a rune in half on the way out.
	maxSnippetChars = 500
)

// truncateRunes shortens s to at most n runes, never splitting one.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// ErrSearchBackend is the class every SearXNG failure belongs to. The general
// rule apply_web_search_ratelimit_patch.py already established for the fork
// applies here: an unavailable backend is an error, never an empty result
// set.
var ErrSearchBackend = errors.New("webtools: search backend unavailable")

// Searcher is the one dependency the web_search handler has.
type Searcher interface {
	// Search returns at most n hits, the count of candidates that could not
	// be carried, and an error. Zero hits with a nil error means the backend
	// answered and had nothing, which is a different outcome from a failure.
	Search(ctx context.Context, query string, n int) ([]Hit, int, error)
}

// SearXNG queries the self-hosted SearXNG service.
type SearXNG struct {
	queryURL string
	timeout  time.Duration
	client   *http.Client
}

// NewSearXNG builds a client for a SearXNG /search endpoint.
//
// Deliberately NOT SafeClient: SearXNG is this stack's own service on the
// compose network, at a private address that SafeClient exists to refuse.
// SafeClient screens agent-supplied URLs; the address of our own backend is
// operator-supplied configuration, not model input.
func NewSearXNG(queryURL string) *SearXNG {
	return &SearXNG{
		queryURL: strings.TrimSpace(queryURL),
		timeout:  DefaultSearchTimeout,
		client:   &http.Client{},
	}
}

type searxngResponse struct {
	Results []struct {
		URL     string  `json:"url"`
		Title   string  `json:"title"`
		Content string  `json:"content"`
		Score   float64 `json:"score"`
	} `json:"results"`
}

// Search runs one query and returns the backend's own ranking.
func (s *SearXNG) Search(ctx context.Context, query string, n int) ([]Hit, int, error) {
	if n <= 0 {
		n = DefaultMaxResults
	}
	if n > MaxResultsCeiling {
		n = MaxResultsCeiling
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	endpoint, err := url.Parse(s.queryURL)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: query url", ErrSearchBackend)
	}
	// Mirrors the parameter set the fork's own SearXNG adapter sends
	// (retrieval/web/searxng.py). format=json is not optional: SearXNG ships
	// with only the html formatter enabled and 403s a JSON request unless
	// deploy/searxng/settings.yml allows it, which it does.
	q := url.Values{}
	q.Set("q", query)
	q.Set("format", "json")
	q.Set("pageno", "1")
	q.Set("safesearch", "1")
	q.Set("language", "all")
	q.Set("theme", "simple")
	q.Set("image_proxy", "0")
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: request", ErrSearchBackend)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Hive web_search")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrSearchBackend, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, 0, fmt.Errorf("%w: status %d", ErrSearchBackend, resp.StatusCode)
	}

	// The response is a snippet list, not a page: a few tens of KiB. Cap it
	// anyway, so a misconfigured backend cannot buffer without limit.
	var payload searxngResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, 0, fmt.Errorf("%w: unparseable response", ErrSearchBackend)
	}

	raw := payload.Results
	sort.SliceStable(raw, func(i, j int) bool { return raw[i].Score > raw[j].Score })

	// No capacity hint. n is clamped to MaxResultsCeiling above, but it still
	// originates in the request body, and a caller-derived value reaching a
	// make cap is worth neither the CodeQL alert it raises nor an argument
	// about whether the clamp is close enough to the allocation to count.
	// Appending at most MaxResultsCeiling elements costs nothing.
	var hits []Hit
	dropped := 0
	for _, item := range raw {
		title := strings.TrimSpace(item.Title)
		snippet := strings.TrimSpace(item.Content)
		// A hit the envelope could not carry is counted, never silently
		// rounded away: a partial loss has to read as a partial loss.
		// Admit is applied here too, so the model is never shown a URL
		// web_fetch would refuse.
		if title == "" || snippet == "" {
			dropped++
			continue
		}
		if _, err := Admit(item.URL); err != nil {
			dropped++
			continue
		}
		snippet = truncateRunes(snippet, maxSnippetChars)
		hits = append(hits, Hit{Title: title, URL: item.URL, Snippet: snippet})
		if len(hits) == n {
			break
		}
	}
	return hits, dropped, nil
}
