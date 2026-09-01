package webtools

import (
	"encoding/json"
	"errors"
	"testing"
	"unicode/utf8"
)

// A success envelope with zero items must not be constructible. This is the
// whole point of the exercise: issue #1609 shipped `status: True` over a
// collection nothing had been written to, so "dropped every source but
// reported success" was a representable state. Here it is not.
func TestNewSearchResultRefusesEmpty(t *testing.T) {
	if _, err := NewSearchResult("anything", nil, 0); !errors.Is(err, ErrEmptyResult) {
		t.Fatalf("NewSearchResult with no hits: want ErrEmptyResult, got %v", err)
	}
	if _, err := NewSearchResult("anything", []Hit{}, 3); !errors.Is(err, ErrEmptyResult) {
		t.Fatalf("NewSearchResult with an empty slice: want ErrEmptyResult, got %v", err)
	}
}

// B1. The same invariant on the fetch side, asserted here because types.go
// owns both constructors even though the pipeline that calls this one is S2.
func TestNewFetchResultRefusesEmpty(t *testing.T) {
	if _, err := NewFetchResult(FetchMeta{URL: "https://example.com"}, nil); !errors.Is(err, ErrEmptyResult) {
		t.Fatalf("NewFetchResult with no parts: want ErrEmptyResult, got %v", err)
	}
}

// retrieved_chars is a character count, matching Part.Start and Part.End. A
// byte count reports a Bangla page as three times its real length, which is
// the figure the model reads when deciding whether it has enough of the page.
func TestNewFetchResultCountsCharactersNotBytes(t *testing.T) {
	const bangla = "খবরের কাগজ"
	got, err := NewFetchResult(FetchMeta{URL: "https://example.com"}, []Part{
		{Text: bangla, Start: 0, End: utf8.RuneCountInString(bangla)},
	})
	if err != nil {
		t.Fatalf("NewFetchResult: %v", err)
	}
	want := utf8.RuneCountInString(bangla)
	if len(bangla) == want {
		t.Fatal("the fixture is single-byte, so it cannot distinguish the two counts")
	}
	if got.RetrievedChars != want {
		t.Fatalf("RetrievedChars = %d, want %d (byte length is %d)", got.RetrievedChars, want, len(bangla))
	}
}

// A1's field requirement, enforced at the only place a success envelope can
// be built, so no caller can forget it.
func TestNewSearchResultRefusesIncompleteHit(t *testing.T) {
	for _, tc := range []struct {
		name string
		hit  Hit
	}{
		{"no title", Hit{URL: "https://example.com/a", Snippet: "s"}},
		{"no url", Hit{Title: "t", Snippet: "s"}},
		{"no snippet", Hit{Title: "t", URL: "https://example.com/a"}},
		{"relative url", Hit{Title: "t", URL: "/a", Snippet: "s"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewSearchResult("q", []Hit{tc.hit}, 0); err == nil {
				t.Fatal("want an error, got a usable envelope")
			}
		})
	}
}

func TestNewSearchResultRanksPositionally(t *testing.T) {
	got, err := NewSearchResult("q", []Hit{
		{Title: "a", URL: "https://example.com/a", Snippet: "s", Rank: 99},
		{Title: "b", URL: "https://example.com/b", Snippet: "s"},
	}, 2)
	if err != nil {
		t.Fatalf("NewSearchResult: %v", err)
	}
	if got.Status != StatusOK {
		t.Fatalf("status = %q, want %q", got.Status, StatusOK)
	}
	if got.Results[0].Rank != 1 || got.Results[1].Rank != 2 {
		t.Fatalf("ranks = %d,%d, want 1,2", got.Results[0].Rank, got.Results[1].Rank)
	}
	if got.Dropped != 2 {
		t.Fatalf("dropped = %d, want 2", got.Dropped)
	}
}

// A3. Zero results from a backend that answered is a distinct value from ok,
// and it must survive serialization as such: the shim renders on this field.
func TestEmptySearchResultIsNotOK(t *testing.T) {
	env := EmptySearchResult("nothing matches this", 0)
	if env.Status != StatusEmpty {
		t.Fatalf("status = %q, want %q", env.Status, StatusEmpty)
	}
	blob, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(blob, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round["status"] != StatusEmpty {
		t.Fatalf("serialized status = %v, want %q", round["status"], StatusEmpty)
	}
}
