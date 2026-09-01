package webtools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

const searxngPayload = `{"results":[
  {"url":"https://example.com/low","title":"Low","content":"low score","score":0.2},
  {"url":"https://example.com/high","title":"High","content":"high score","score":9.5},
  {"url":"https://example.com/mid","title":"Mid","content":"mid score","score":3.0}
]}`

// A1. Every hit carries a non-empty title, an absolute URL and a non-empty
// snippet, and the backend's own score ordering is preserved.
func TestSearchReturnsRankedHits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("format"); got != "json" {
			t.Errorf("format = %q, want json", got)
		}
		if got := r.URL.Query().Get("q"); got != "who won" {
			t.Errorf("q = %q, want %q", got, "who won")
		}
		_, _ = w.Write([]byte(searxngPayload))
	}))
	defer srv.Close()

	hits, dropped, err := NewSearXNG(srv.URL).Search(context.Background(), "who won", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(hits) != 3 {
		t.Fatalf("got %d hits, want 3", len(hits))
	}
	if hits[0].URL != "https://example.com/high" {
		t.Fatalf("first hit = %q, want the highest scoring result", hits[0].URL)
	}
	for i, h := range hits {
		if h.Title == "" || h.URL == "" || h.Snippet == "" {
			t.Fatalf("hit %d has an empty field: %+v", i, h)
		}
		if !strings.HasPrefix(h.URL, "https://") {
			t.Fatalf("hit %d URL %q is not absolute", i, h.URL)
		}
	}
}

// A2 and A8. Searching makes exactly one outbound HTTP request: no page is
// fetched and no model is called. This is the differentiator the whole spec
// turns on, and the structural fix for #1609's five fetches per search.
func TestSearchMakesExactlyOneOutboundRequest(t *testing.T) {
	var outbound atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		outbound.Add(1)
		_, _ = w.Write([]byte(searxngPayload))
	}))
	defer srv.Close()

	if _, _, err := NewSearXNG(srv.URL).Search(context.Background(), "who won", 5); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := outbound.Load(); got != 1 {
		t.Fatalf("the search path made %d outbound requests, want exactly 1", got)
	}
}

func TestSearchHonoursResultCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(searxngPayload))
	}))
	defer srv.Close()

	hits, _, err := NewSearXNG(srv.URL).Search(context.Background(), "q", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(hits))
	}
}

// A hit the envelope could not carry is counted, never silently rounded away.
// Partial loss is reported as partial loss.
func TestSearchCountsUnusableHitsAsDropped(t *testing.T) {
	const payload = `{"results":[
	  {"url":"https://example.com/ok","title":"Ok","content":"fine","score":1},
	  {"url":"https://example.com/nosnippet","title":"No snippet","content":"","score":1},
	  {"url":"","title":"No url","content":"x","score":1},
	  {"url":"http://169.254.169.254/latest","title":"Metadata","content":"x","score":1}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	hits, dropped, err := NewSearXNG(srv.URL).Search(context.Background(), "q", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(hits))
	}
	if dropped != 3 {
		t.Fatalf("dropped = %d, want 3", dropped)
	}
}

// An over-long snippet is trimmed on a character boundary, not a byte one. A
// byte cut through a multi-byte character produces mojibake in the tool
// result the model reads, and Bangla, which this product's first market
// writes in, is three bytes per character throughout.
func TestSearchTrimsSnippetsOnCharacterBoundaries(t *testing.T) {
	long := strings.Repeat("খ", maxSnippetChars+50)
	payload := `{"results":[{"url":"https://example.com/a","title":"T","content":"` + long + `","score":1}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	hits, _, err := NewSearXNG(srv.URL).Search(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(hits))
	}
	if !utf8.ValidString(hits[0].Snippet) {
		t.Fatal("the trimmed snippet is not valid UTF-8; the cut split a character")
	}
	if got := utf8.RuneCountInString(hits[0].Snippet); got != maxSnippetChars {
		t.Fatalf("the trimmed snippet is %d characters, want %d", got, maxSnippetChars)
	}
}

// A snippet is attacker-influenceable text entering the model's context, and
// invisible characters are the standard way to hide an instruction from a
// human reviewer while leaving it legible to the model. Issue #1640 is exactly
// this channel.
func TestSearchStripsInvisibleCharactersFromHits(t *testing.T) {
	// Zero width space, zero width non-joiner, right-to-left override,
	// left-to-right isolate, byte order mark and a NUL, interleaved with the
	// text a reader would actually see. Written as JSON escapes rather than Go
	// ones: the decoder is what turns them into real characters, and an
	// unescaped control character would make the fixture invalid JSON.
	payload := `{"results":[{"url":"https://example.com/a",` +
		`"title":"Real\u200btitle\u200c with\u202e hidden\u2066 text\ufeff",` +
		`"content":"Snippet\u200b with\u202e marks","score":1}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	hits, _, err := NewSearXNG(srv.URL).Search(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(hits))
	}
	for _, field := range []string{hits[0].Title, hits[0].Snippet} {
		for _, r := range field {
			if r == '\u200b' || r == '\u200c' || r == '\u202e' || r == '\u2066' || r == '\ufeff' || r == 0 {
				t.Fatalf("invisible character %U survived in %q", r, field)
			}
		}
	}
	// The legible text has to survive, or stripping has eaten the result.
	if !strings.Contains(hits[0].Title, "Real") || !strings.Contains(hits[0].Title, "text") {
		t.Fatalf("stripping removed visible text: %q", hits[0].Title)
	}
}

// A hit whose title is nothing but invisible characters carries no title at
// all once stripped, so it is dropped rather than shown as a blank citation.
func TestSearchDropsHitsThatAreInvisibleOnly(t *testing.T) {
	payload := `{"results":[{"url":"https://example.com/a","title":"\u200b\u200c","content":"real","score":1}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	hits, dropped, err := NewSearXNG(srv.URL).Search(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 0 || dropped != 1 {
		t.Fatalf("hits = %d, dropped = %d, want 0, 1", len(hits), dropped)
	}
}

// A4. An unavailable backend is an error, never an empty result set. This is
// the rule apply_web_search_ratelimit_patch.py already established.
func TestSearchBackendErrorIsNotAnEmptyResultSet(t *testing.T) {
	for _, status := range []int{429, 500, 502, 403} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		hits, _, err := NewSearXNG(srv.URL).Search(context.Background(), "q", 5)
		srv.Close()
		if err == nil {
			t.Fatalf("status %d: Search returned %d hits and no error", status, len(hits))
		}
		if !errors.Is(err, ErrSearchBackend) {
			t.Fatalf("status %d: error = %v, want ErrSearchBackend", status, err)
		}
	}
}

func TestSearchUnparseableBodyIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>not json</html>"))
	}))
	defer srv.Close()

	if _, _, err := NewSearXNG(srv.URL).Search(context.Background(), "q", 5); err == nil {
		t.Fatal("an unparseable backend response was reported as success")
	}
}

// A4's timeout half. A backend that never answers is an error envelope, not a
// call that hangs for the length of the chat turn.
func TestSearchTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	// Close waits for outstanding handlers, so release has to run first.
	// Deferred calls are last-in-first-out.
	defer srv.Close()
	defer close(release)

	client := NewSearXNG(srv.URL)
	client.timeout = 50 * time.Millisecond
	start := time.Now()
	if _, _, err := client.Search(context.Background(), "q", 5); err == nil {
		t.Fatal("a search against a server that never answers returned successfully")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the search timeout took %s to fire", elapsed)
	}
}

// A zero-result answer from a backend that did answer is not an error, and it
// is not a hit list either. The handler turns this into status "empty".
func TestSearchZeroResultsIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	hits, dropped, err := NewSearXNG(srv.URL).Search(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 0 || dropped != 0 {
		t.Fatalf("hits = %d, dropped = %d, want 0, 0", len(hits), dropped)
	}
}
