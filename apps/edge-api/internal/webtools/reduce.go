package webtools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Stage 4 of the web_fetch pipeline: turn a whole page into the few parts
// worth putting in front of the model.
//
// The shape of this file is issue #1609's own ordering, and every constant
// below is one of its four rules:
//
//   - Batch. Every chunk of a page goes in ONE embedding request as an input
//     array, plus one for the focus string. Two per call, fixed, independent
//     of page size. A 200 chunk page is 2 requests, not 200 (criterion B7).
//   - Bound. A package semaphore over concurrent embedding calls, default 4.
//     Zero is refused rather than obeyed, because zero means unbounded, which
//     is the defect.
//   - Cap the input. At most MaxChunksPerPage chunks; past that a head and
//     tail window, rather than a request that grows without limit.
//   - Do not touch the rate limit. Nothing here raises a ceiling. Two calls
//     instead of two hundred removes the hold pressure by that factor, which
//     is the whole fix.
//
// Nothing is written anywhere. A fetched page is not durable knowledge, so
// there is no collection, no cleanup job, no RLS surface and no second store
// to disagree with the first (spec decision D-E).

const (
	// ChunkChars is the chunk width in characters, and ChunkOverlapChars the
	// overlap between neighbours so a sentence split across a boundary is
	// still whole in one of them. Characters, not bytes: Bangladesh is this
	// product's first market and a Bangla character is three bytes, so a byte
	// width would chunk a Bangla page three times as finely as an English one
	// and could cut a rune in half.
	ChunkChars        = 2000
	ChunkOverlapChars = 200

	// MaxCallChars is both the small-page threshold and the per-call ceiling.
	// Under it a page is handed over whole and costs no embedding at all;
	// over it, ranked parts are taken until this many characters are spent.
	// One number for both, so "small enough to return whole" and "as much as
	// one call may return" cannot drift apart.
	MaxCallChars = 12000
	// MaxParts bounds how many separate spans a call returns. MaxCallChars
	// alone would allow a large number of tiny ones.
	MaxParts = 6

	// MaxChunksPerPage caps the embedding request's input array. A page
	// needing more than this is not a page the model should be reading whole.
	MaxChunksPerPage = 256

	// DefaultEmbedConcurrency bounds concurrent embedding calls across all
	// in-flight tool calls.
	DefaultEmbedConcurrency = 4
)

var (
	// ErrEmbedUnavailable is returned when the embedding backend failed, was
	// rate limited, or is not wired at all. It is never a partial success:
	// criterion B8 is that a 429 produces this and no envelope.
	ErrEmbedUnavailable = errors.New("webtools: embedding backend unavailable")
	// ErrReduceEmpty is a ranking that selected nothing. Distinct from
	// ErrExtractEmpty: the page had text and the reduction still produced no
	// part, which is a different fact and a different message.
	ErrReduceEmpty = errors.New("webtools: reduction selected no content")
)

// BatchEmbedder embeds a batch of texts in one request. Declared here, where
// it is used, rather than in the rag package that implements it.
type BatchEmbedder interface {
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// Reducer ranks a page's chunks against the focus string.
type Reducer struct {
	embed BatchEmbedder
	// sem bounds concurrent embedding calls. A buffered channel rather than a
	// semaphore package: two selects and a cap is the whole requirement.
	sem chan struct{}
}

// NewReducer builds a Reducer. A non-positive concurrency is refused, not
// defaulted: zero means no bound at all, which is precisely the unbounded
// burst the bound exists to end, and silently substituting 4 for it would
// leave an operator believing a value that was never obeyed.
func NewReducer(embed BatchEmbedder, concurrency int) (*Reducer, error) {
	if concurrency < 1 {
		return nil, fmt.Errorf("webtools: embedding concurrency must be at least 1, got %d", concurrency)
	}
	return &Reducer{embed: embed, sem: make(chan struct{}, concurrency)}, nil
}

// Reduce returns the parts of doc worth showing, whether the page was cut
// short, and how many chunks were lost on the way.
//
// Three paths, and only the third spends anything:
//
//  1. A page under MaxCallChars is returned whole, with no embedding call at
//     all. This is the common case.
//  2. A larger page with no focus string is windowed from the top. There is
//     nothing to rank against, and embedding against an empty query would be
//     a billed call that answers nothing.
//  3. A larger page with a focus string is chunked, embedded in two requests
//     and ranked by cosine.
func (r *Reducer) Reduce(ctx context.Context, doc Doc, focus string) ([]Part, bool, int, error) {
	runes := []rune(doc.Text)
	if len(runes) == 0 {
		return nil, false, 0, ErrExtractEmpty
	}

	token := fenceToken()

	if len(runes) <= MaxCallChars {
		return []Part{fenced(Part{Text: string(runes), Start: 0, End: len(runes)}, token)}, false, 0, nil
	}

	chunks := chunkRunes(runes)
	if len(chunks) == 0 {
		return nil, false, 0, ErrReduceEmpty
	}

	focus = strings.TrimSpace(focus)
	if focus == "" {
		selected, dropped := takeInOrder(chunks)
		if len(selected) == 0 {
			// Not reachable while a chunk is narrower than the per-call
			// ceiling, which chunkRunes guarantees. Guarded anyway, because
			// the alternative is returning zero parts with a nil error, and
			// "no content, reported as success" is the one shape this package
			// exists to make unrepresentable.
			return nil, false, 0, ErrReduceEmpty
		}
		return fenceAll(selected, token), true, dropped, nil
	}

	if r.embed == nil {
		return nil, false, 0, fmt.Errorf("%w: no embedder is wired", ErrEmbedUnavailable)
	}

	windowed, windowDropped := windowChunks(chunks)

	scores, err := r.score(ctx, windowed, focus)
	if err != nil {
		return nil, false, 0, err
	}

	ranked := make([]int, len(windowed))
	for i := range ranked {
		ranked[i] = i
	}
	sort.SliceStable(ranked, func(a, b int) bool { return scores[ranked[a]] > scores[ranked[b]] })

	var picked []Part
	spent := 0
	for _, idx := range ranked {
		part := windowed[idx]
		width := part.End - part.Start
		if len(picked) >= MaxParts || spent+width > MaxCallChars {
			continue
		}
		picked = append(picked, part)
		spent += width
	}
	if len(picked) == 0 {
		return nil, false, 0, ErrReduceEmpty
	}
	// Back into document order. Ranked order is how they were chosen; page
	// order is how they are read, and a model quoting a page it was shown
	// backwards quotes it badly.
	sort.SliceStable(picked, func(a, b int) bool { return picked[a].Start < picked[b].Start })

	return fenceAll(picked, token), true, windowDropped + len(windowed) - len(picked), nil
}

// score embeds the focus string and every chunk, in exactly two requests, and
// returns the cosine similarity of each chunk to the focus.
func (r *Reducer) score(ctx context.Context, chunks []Part, focus string) ([]float64, error) {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		// The cause is named with %v, not wrapped with %w, deliberately.
		// Wrapping puts context.DeadlineExceeded in the chain, and fetchCode
		// asks for that before it asks for ErrEmbedUnavailable, so waiting
		// too long for the embedding bound would be reported as fetch_timeout
		// and the user would be told the page was slow when the page had
		// already arrived. Same class collapse as the redirect ordering above
		// it; the fix is to keep the class out of the chain rather than to
		// keep reordering the switch.
		return nil, fmt.Errorf("%w: waiting for the embedding bound: %v", ErrEmbedUnavailable, ctx.Err())
	}

	// The focus first, and alone: it is the cheap call, so a backend that is
	// down costs one small request rather than one large one.
	focusVecs, err := r.embed.EmbedBatch(ctx, []string{focus})
	if err != nil || len(focusVecs) != 1 {
		return nil, embedFailure(err, len(focusVecs), 1)
	}

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}
	chunkVecs, err := r.embed.EmbedBatch(ctx, texts)
	if err != nil || len(chunkVecs) != len(chunks) {
		return nil, embedFailure(err, len(chunkVecs), len(chunks))
	}

	scores := make([]float64, len(chunks))
	for i, vec := range chunkVecs {
		if len(vec) != len(focusVecs[0]) {
			// A backend answering at two widths in one call is broken, and
			// ranking on a silently zeroed score would hide it.
			return nil, fmt.Errorf("%w: inconsistent vector widths", ErrEmbedUnavailable)
		}
		scores[i] = cosine(focusVecs[0], vec)
	}
	return scores, nil
}

// embedFailure turns either failure mode into one class. The cause is wrapped
// for the log; the handler emits a fixed message, so nothing an upstream said
// reaches the client (criterion B11).
func embedFailure(err error, got, want int) error {
	if err != nil {
		// %v rather than %w, for the reason given at the semaphore wait: an
		// embedding call that timed out is embed_unavailable, and wrapping
		// its context.DeadlineExceeded would report it as the page being slow.
		return fmt.Errorf("%w: %v", ErrEmbedUnavailable, err)
	}
	return fmt.Errorf("%w: got %d vectors for %d inputs", ErrEmbedUnavailable, got, want)
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// chunkRunes splits the page into overlapping windows, carrying the character
// offsets each one occupied so a quote can be located in the page.
//
// ponytail: fixed-width windows on a rune slice, with the cut nudged back to
// the nearest whitespace so a chunk does not start mid-word. Not a sentence
// splitter: the overlap already covers a sentence that straddles a boundary,
// and the ranking is over embeddings rather than over grammar.
func chunkRunes(runes []rune) []Part {
	var parts []Part
	for start := 0; start < len(runes); {
		end := start + ChunkChars
		if end >= len(runes) {
			end = len(runes)
		} else {
			end = backUpToWhitespace(runes, start, end)
		}
		text := strings.TrimSpace(string(runes[start:end]))
		if text != "" {
			parts = append(parts, Part{Text: text, Start: start, End: end})
		}
		if end >= len(runes) {
			break
		}
		next := end - ChunkOverlapChars
		if next <= start {
			next = end
		}
		start = next
	}
	return parts
}

// backUpToWhitespace moves a cut back to the last space in the final tenth of
// the window, so chunks break between words. It never moves more than that,
// so a run of text with no spaces still advances.
func backUpToWhitespace(runes []rune, start, end int) int {
	limit := end - ChunkChars/10
	if limit < start+1 {
		limit = start + 1
	}
	for i := end - 1; i >= limit; i-- {
		switch runes[i] {
		case ' ', '\n', '\t':
			return i + 1
		}
	}
	return end
}

// windowChunks caps the input array at MaxChunksPerPage by keeping a head and
// a tail window, which is where a document says what it is and what it
// concluded. Returns the kept chunks and how many were dropped.
func windowChunks(chunks []Part) ([]Part, int) {
	if len(chunks) <= MaxChunksPerPage {
		return chunks, 0
	}
	head := MaxChunksPerPage / 2
	tail := MaxChunksPerPage - head
	kept := make([]Part, 0, MaxChunksPerPage)
	kept = append(kept, chunks[:head]...)
	kept = append(kept, chunks[len(chunks)-tail:]...)
	return kept, len(chunks) - MaxChunksPerPage
}

// takeInOrder is the no-focus path: the first parts of the page, up to the
// per-call ceiling.
func takeInOrder(chunks []Part) ([]Part, int) {
	var picked []Part
	spent := 0
	for _, c := range chunks {
		width := c.End - c.Start
		if len(picked) >= MaxParts || spent+width > MaxCallChars {
			break
		}
		picked = append(picked, c)
		spent += width
	}
	return picked, len(chunks) - len(picked)
}

// The untrusted-content fence (criterion B10, spec section 7's prompt
// injection containment).
//
// Every returned span is wrapped in a marker carrying a token drawn fresh per
// call. A page cannot close a fence whose token it has never seen, so text it
// controls cannot present itself as being outside the fence and addressing
// the model as its operator. The tool description states that everything
// inside is data and never instruction.
//
// Not claimed as a solution to prompt injection, which has none. It reduces
// blast radius and makes the attempt legible.
const (
	fenceOpenPrefix  = "[BEGIN UNTRUSTED WEB CONTENT "
	fenceClosePrefix = "[END UNTRUSTED WEB CONTENT "
	fenceSuffix      = "]"
)

func fenceToken() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice, and a predictable token is
		// worse than no token, so this degrades to a fence that says so
		// rather than to a guessable one.
		return "unavailable"
	}
	return hex.EncodeToString(b[:])
}

func fenced(p Part, token string) Part {
	p.Text = fenceOpenPrefix + token + fenceSuffix + "\n" + p.Text + "\n" + fenceClosePrefix + token + fenceSuffix
	return p
}

// fenceAll wraps every part with the call's token.
//
// Note for anyone reading FetchResult.RetrievedChars: it counts what is
// returned, fences included, because NewFetchResult counts the runes of
// Part.Text and the fence is part of that text. The overhead is a fixed few
// dozen characters per part.
func fenceAll(parts []Part, token string) []Part {
	out := make([]Part, len(parts))
	for i, p := range parts {
		out[i] = fenced(p, token)
	}
	return out
}
