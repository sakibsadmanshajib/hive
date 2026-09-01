package webtools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
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
	// The figure is the spec's (section 8).
	//
	// Two consequences, both stated rather than left to be discovered.
	//
	// The backend must accept an input array this long in one request. At
	// ChunkChars = 2000 that is up to 512,000 characters in a single POST to
	// /v1/embeddings. Several OpenAI-compatible backends cap inputs per
	// request well below 256, or cap total request tokens, and LiteLLM passes
	// the provider's refusal straight through, so on such a backend every
	// large page fails with embed_unavailable while every small page works.
	// That shape is hard to read from outside, so the refusal is logged with
	// the input count (see embedFailure) and the first thing to check is this
	// number against the backend's own per-request limit. There is
	// deliberately no automatic split-and-retry: it would be untested against
	// any backend we actually run, and a retry path that only ever executes
	// during an outage is a retry path nobody has seen work.
	//
	// The embedding is not metered. It goes through the same client and the
	// same LiteLLM master key as a RAG query embedding, which also takes no
	// hold, but the volume is different in kind: a RAG query embeds one short
	// string, and this embeds up to 512,000 characters per large page. With
	// TenantCallsPerMinute at 30 that is roughly 15 million characters of
	// embedding input per tenant per minute, absorbed rather than billed, and
	// bounded only by that rate limit. That is a deliberate decision for this
	// slice and not an oversight: metering it needs a hold and a settlement on
	// a path that has neither, which is its own change. Given D-031 through
	// D-034, an unstated money assumption is the thing to avoid here, so it is
	// stated. Revisit when web_fetch is advertised to the API-key surface,
	// where the volume stops being bounded by one chat deployment.
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

	token, err := fenceToken()
	if err != nil {
		return nil, false, 0, err
	}

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
		// Head-window chunks are adjacent by construction, so every pair
		// repeats ChunkOverlapChars until this runs.
		selected = trimOverlap(runes, selected)
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
		// The fence is counted. What is returned is fenced(part), so a budget
		// spent on the raw window width is not the budget the caller was told
		// about: six parts overshot MaxCallChars by 544 runes before this,
		// and this constant is doing injection containment work in its own
		// comment, so the stated bound and the shipped bound should be one
		// number.
		width := part.End - part.Start + fenceOverheadChars
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
	picked = trimOverlap(runes, picked)
	if len(picked) == 0 {
		return nil, false, 0, ErrReduceEmpty
	}

	return fenceAll(picked, token), true, windowDropped + len(windowed) - len(picked), nil
}

// trimOverlap removes the duplicated head of any selected part that starts
// inside its predecessor.
//
// ChunkOverlapChars exists so a sentence straddling a boundary stays whole in
// one chunk, which is right for ranking and wrong for the returned span:
// neighbouring text scores alike, so ranking usually picks adjacent chunks and
// each adjacent pair then repeats the overlap. Measured on a 50,000 rune page
// that was 1,000 of the 12,000 character budget spent showing the model text
// it had already been shown.
//
// The trim re-slices the original rune slice rather than cutting the chunk's
// own Text, so Start stays exactly the offset the text begins at. Cutting the
// string would have been approximate, because chunkRunes trims whitespace off
// each window and the string is therefore not always End minus Start long.
func trimOverlap(runes []rune, parts []Part) []Part {
	out := make([]Part, 0, len(parts))
	prevEnd := 0
	for _, p := range parts {
		if p.Start < prevEnd {
			p.Start = prevEnd
		}
		if p.Start >= p.End {
			// Fully covered by its predecessor. Dropping it is right; the
			// caller counts it in dropped like any other unselected chunk.
			continue
		}
		p.Text = strings.TrimSpace(string(runes[p.Start:p.End]))
		if p.Text == "" {
			continue
		}
		prevEnd = p.End
		out = append(out, p)
	}
	return out
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
	// From here the failure could be the backend refusing an input array of
	// this length rather than being down, and the two are indistinguishable in
	// the client message by design (criterion B11). The log is where they are
	// told apart, so the count goes in it.

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}
	chunkVecs, err := r.embed.EmbedBatch(ctx, texts)
	if err != nil || len(chunkVecs) != len(chunks) {
		log.Printf("webtools: embedding a batch of %d chunks failed; if small pages work and large ones do not, check this count against the backend's per-request input limit: %v",
			len(texts), err)
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
		// The fence counted, as on the ranked path, so both paths spend the
		// same budget on the same thing.
		width := c.End - c.Start + fenceOverheadChars
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
	// fenceTokenBytes is the token width. Hex encoded, so the token is twice
	// this many characters.
	fenceTokenBytes = 8
)

// fenceOverheadChars is what fencing adds to a part, in characters. Every
// component is ASCII, so bytes and runes agree. Counted against MaxCallChars
// so the budget covers what is actually returned.
const fenceOverheadChars = len(fenceOpenPrefix) + len(fenceClosePrefix) +
	4*fenceTokenBytes + 2*len(fenceSuffix) + 2 // two tokens hex encoded, two newlines

// fenceToken draws one call's fence token.
//
// It returns an error rather than degrading to a constant. A previous version
// fell back to the literal "unavailable", which is guessable, and a guessable
// token is not a fence: a page carrying the matching close marker escapes it
// and addresses the model from outside, which is the single property the fence
// exists to deny. Unreachable in practice, since crypto/rand.Read does not
// fail on any platform this runs on, but the safe failure for a security
// primitive is to fail rather than to weaken.
func fenceToken() (string, error) {
	var b [fenceTokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("webtools: no random source for the content fence: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
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
