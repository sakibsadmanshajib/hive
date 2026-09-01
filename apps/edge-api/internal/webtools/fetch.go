package webtools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/rag"
)

// The web_fetch pipeline: stages 1 through 5, over the admission (stage 0)
// and the safe dialer slice S1 already put in front of this route.
//
// The tool this belongs to takes a URL and breaks it down, which is the whole
// distinction from web_search: search handles query strings, fetch handles
// URLs. Every stage has exactly one failure envelope shape, every failure is
// visible to the model, and no stage can hand the next one an empty success.
// A page that produced nothing is an error class naming which stage produced
// nothing, never a 200 carrying no content, which is the shape of #1609.

// ErrFetchStatus is an upstream non-2xx. Its own class, carrying the status,
// because "the page said 404" and "the page had no readable text" are
// different facts and a model told the wrong one answers wrongly.
var ErrFetchStatus = errors.New("webtools: upstream returned an error status")

// fetchDetail carries the one machine fact a failure class is allowed to
// report back to the caller: an HTTP status, or a media type that has already
// been shape-checked. Everything else about a failure goes to the log.
//
// It exists so criterion B9's classes stay distinct while the messages stay
// specific. errors.Is finds the class through Unwrap; errors.As finds the
// detail. Nothing here interpolates an internal error, an internal service
// name or a resolved address, per criterion B11.
type fetchDetail struct {
	class     error
	status    int
	mediaType string
}

func (e *fetchDetail) Error() string {
	if e.status != 0 {
		return fmt.Sprintf("%v (status %d)", e.class, e.status)
	}
	if e.mediaType != "" {
		return fmt.Sprintf("%v (%s)", e.class, e.mediaType)
	}
	return e.class.Error()
}

func (e *fetchDetail) Unwrap() error { return e.class }

// upstreamStatusOf reports the HTTP status a fetch_status error carries, or 0.
func upstreamStatusOf(err error) int {
	var d *fetchDetail
	if errors.As(err, &d) {
		return d.status
	}
	return 0
}

// mediaTypeOf reports the media type an unsupported_content_type error
// carries, or "".
func mediaTypeOf(err error) string {
	var d *fetchDetail
	if errors.As(err, &d) {
		return d.mediaType
	}
	return ""
}

// PipelineConfig configures NewPipeline. Every field has a working default
// except Embedder, whose absence makes a large page an explicit
// embed_unavailable rather than a silently truncated answer.
type PipelineConfig struct {
	// Client is the HTTP client for agent-supplied URLs. Nil means a fresh
	// SafeClient, which is the only client this pipeline should ever use in
	// production: it resolves and screens at connect time and re-admits every
	// redirect hop.
	Client *http.Client
	// Converter is the markitdown sidecar for the binary branch.
	Converter rag.Converter
	// Embedder is the batched embedding backend for the ranking path.
	Embedder BatchEmbedder
	// MaxBytes is the response byte cap. Zero means MaxFetchBytes.
	MaxBytes int64
	// EmbedConcurrency bounds concurrent embedding calls. Zero means
	// DefaultEmbedConcurrency; a negative value is refused.
	EmbedConcurrency int
}

// Pipeline implements Fetcher.
type Pipeline struct {
	client  *http.Client
	extract Extractor
	reduce  *Reducer
}

// NewPipeline builds the web_fetch pipeline.
func NewPipeline(cfg PipelineConfig) (*Pipeline, error) {
	client := cfg.Client
	if client == nil {
		client = SafeClient(ClientConfig{})
	}
	concurrency := cfg.EmbedConcurrency
	if concurrency == 0 {
		concurrency = DefaultEmbedConcurrency
	}
	reducer, err := NewReducer(cfg.Embedder, concurrency)
	if err != nil {
		return nil, err
	}
	return &Pipeline{
		client:  client,
		extract: Extractor{Converter: cfg.Converter, MaxBytes: cfg.MaxBytes},
		reduce:  reducer,
	}, nil
}

// Fetch runs the pipeline over one URL.
//
// It deliberately does not re-run Admit on target. The handler admits before
// it delegates, and the enforcement that actually matters is at connect time:
// the client's dialer screens the address it is about to connect to and
// CheckRedirect re-admits every hop. A second admission here would be a third
// place for the policy to live and a seam a test would want opened, and an
// SSRF policy with an openable seam is worth less than one without.
func (p *Pipeline) Fetch(ctx context.Context, target, focus string) (FetchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return FetchResult{}, fmt.Errorf("%w: building request: %w", ErrURLRejected, err)
	}
	// Asking for what this pipeline can actually read. A server with a choice
	// is told which branch we would rather take.
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,application/pdf;q=0.8,*/*;q=0.1")
	req.Header.Set("Accept-Language", "en,*;q=0.5")
	req.Header.Set("User-Agent", "Hive web_fetch")

	resp, err := p.client.Do(req)
	if err != nil {
		// The sentinels safedial.go returns survive *url.Error's Unwrap, so
		// fetchCode sees the real class. Nothing is added to the message here
		// because *url.Error stringifies with the URL in it.
		return FetchResult{}, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return FetchResult{}, &fetchDetail{class: ErrFetchStatus, status: resp.StatusCode}
	}

	// A declared length past the cap is refused before the body is read at
	// all. It is only ever a hint (the header is the server's claim, and this
	// one lies in the test that proves it), so the read stays capped too.
	if maxBytes := p.extract.max(); resp.ContentLength > maxBytes {
		return FetchResult{}, ErrTooLarge
	}

	doc, err := p.extract.Extract(ctx, resp.Header.Get("Content-Type"), req.URL.Path, resp.Body)
	if err != nil {
		return FetchResult{}, err
	}

	parts, truncated, dropped, err := p.reduce.Reduce(ctx, doc, focus)
	if err != nil {
		return FetchResult{}, err
	}

	finalURL := target
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}

	// NewFetchResult is the only constructor, and it refuses an empty parts
	// slice. Reduce cannot return one without an error, and this is the
	// second lock on the same door rather than the first.
	return NewFetchResult(FetchMeta{
		URL:        target,
		FinalURL:   finalURL,
		Title:      doc.Title,
		Truncated:  truncated,
		TotalChars: utf8.RuneCountInString(doc.Text),
		Dropped:    dropped,
	}, parts)
}
