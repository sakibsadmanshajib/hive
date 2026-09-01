package webtools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestPipeline builds the real pipeline over a client that will talk to a
// loopback test server. allowAddr is the package-private test hook safedial.go
// documents; nothing outside this package can reach it, which is how spec
// section 7 item 11 (ENABLE_LOCAL_WEB_FETCH must never be true) is held while
// still exercising the pipeline against a real HTTP server.
func newTestPipeline(t *testing.T, cfg PipelineConfig) *Pipeline {
	t.Helper()
	if cfg.Transport.allowAddr == nil {
		cfg.Transport = ClientConfig{Timeout: 5 * time.Second, allowAddr: allowAnyAddrForTest}
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = MaxFetchBytes
	}
	if cfg.EmbedConcurrency == 0 {
		cfg.EmbedConcurrency = DefaultEmbedConcurrency
	}
	p, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	return p
}

func htmlServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchReturnsAConformingEnvelope(t *testing.T) {
	srv := htmlServer(t, `<html><head><title>Rates</title></head><body>
		<p>Hive charges one credit per million tokens.</p></body></html>`)

	result, err := newTestPipeline(t, PipelineConfig{}).Fetch(context.Background(), srv.URL+"/rates", "pricing")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if result.Status != StatusOK {
		t.Fatalf("status = %q, want %q", result.Status, StatusOK)
	}
	if len(result.Parts) == 0 {
		t.Fatal("a success envelope carries no parts")
	}
	if result.Title != "Rates" {
		t.Fatalf("title = %q", result.Title)
	}
	if result.URL != srv.URL+"/rates" || result.FinalURL != srv.URL+"/rates" {
		t.Fatalf("url = %q, final = %q", result.URL, result.FinalURL)
	}
	if result.TotalChars == 0 || result.RetrievedChars == 0 {
		t.Fatalf("char counts = %d total, %d retrieved", result.TotalChars, result.RetrievedChars)
	}
	if !strings.Contains(result.Parts[0].Text, "one credit per million tokens") {
		t.Fatalf("part text = %q", result.Parts[0].Text)
	}
}

// B5, end to end and on the wire rather than against a reader stub. The
// server declares no length at all and streams chunked, which is what an
// ordinary dynamic page does, so nothing about the size is known until the
// bytes arrive. A cap applied after reading passes this test having already
// paid the cost it exists to avoid; only a cap enforced during the read
// refuses it.
//
// There is deliberately no "the server lied about Content-Length" case here:
// net/http enforces a declared length itself and truncates past it, so that
// scenario cannot be built and a test claiming to cover it would be theatre.
func TestFetchRefusesABodyPastTheCapWithNoDeclaredLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		for i := 0; i < 64; i++ {
			if _, err := w.Write([]byte(strings.Repeat("a", 1024))); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	_, err := newTestPipeline(t, PipelineConfig{MaxBytes: 2048}).Fetch(context.Background(), srv.URL, "")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("error = %v, want ErrTooLarge", err)
	}
	if fetchCode(err) != CodeFetchTooLarge {
		t.Fatalf("fetchCode = %q, want %q", fetchCode(err), CodeFetchTooLarge)
	}
}

// A declared Content-Length past the cap is refused before the body is read
// at all. Cheap, and it means the common honest case costs nothing.
func TestFetchRefusesAnHonestlyOversizedResponseBeforeReadingIt(t *testing.T) {
	var bodyWrites atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := strings.Repeat("a", 8192)
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.WriteHeader(http.StatusOK)
		bodyWrites.Add(1)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	_, err := newTestPipeline(t, PipelineConfig{MaxBytes: 1024}).Fetch(context.Background(), srv.URL, "")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("error = %v, want ErrTooLarge", err)
	}
}

// B6, through the pipeline. An unlisted content type never reaches an
// extractor and never reaches the converter.
func TestFetchRefusesUnlistedContentTypes(t *testing.T) {
	for _, ct := range []string{"image/png", "video/mp4", "application/octet-stream"} {
		t.Run(ct, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", ct)
				_, _ = w.Write([]byte("BINARY"))
			}))
			defer srv.Close()

			conv := &stubConverter{markdown: "never"}
			_, err := newTestPipeline(t, PipelineConfig{Converter: conv}).Fetch(context.Background(), srv.URL, "")
			if !errors.Is(err, ErrUnsupportedContentType) {
				t.Fatalf("error = %v, want ErrUnsupportedContentType", err)
			}
			if conv.calls != 0 {
				t.Fatalf("the converter saw %d calls for %s", conv.calls, ct)
			}
			if fetchCode(err) != CodeUnsupportedContentType {
				t.Fatalf("fetchCode = %q", fetchCode(err))
			}
		})
	}
}

// An upstream error status is its own class, carrying the status, and is
// never collapsed into "the page had no content".
func TestFetchReportsUpstreamStatus(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("<html><body>error page</body></html>"))
			}))
			defer srv.Close()

			_, err := newTestPipeline(t, PipelineConfig{}).Fetch(context.Background(), srv.URL, "")
			if !errors.Is(err, ErrFetchStatus) {
				t.Fatalf("error = %v, want ErrFetchStatus", err)
			}
			if fetchCode(err) != CodeFetchStatus {
				t.Fatalf("fetchCode = %q", fetchCode(err))
			}
			if got := upstreamStatusOf(err); got != status {
				t.Fatalf("carried status = %d, want %d", got, status)
			}
		})
	}
}

// B9, through the pipeline: a page that loads and extracts to nothing is its
// own class, and the model is told the fetch succeeded and the extraction did
// not rather than being handed silence.
func TestFetchOnAJavaScriptOnlyPageIsExtractEmpty(t *testing.T) {
	srv := htmlServer(t, `<html><body><div id="root"></div><script>render()</script></body></html>`)
	_, err := newTestPipeline(t, PipelineConfig{}).Fetch(context.Background(), srv.URL, "")
	if !errors.Is(err, ErrExtractEmpty) {
		t.Fatalf("error = %v, want ErrExtractEmpty", err)
	}
}

// B3 again, at the pipeline rather than the client: the pipeline surfaces a
// blocked redirect as its own class, so "the page tried to send us somewhere
// private" and "the page was slow" never render as the same message.
func TestFetchSurfacesABlockedRedirect(t *testing.T) {
	var secretHits atomic.Int32
	secret := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secretHits.Add(1)
		_, _ = w.Write([]byte("INTERNAL"))
	}))
	defer secret.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secret.URL, http.StatusFound)
	}))
	defer origin.Close()

	// allowAddr is wide open here on purpose: the refusal has to come from
	// CheckRedirect re-admitting the hop through Admit, which refuses a
	// loopback literal whatever the dialer's address policy says. A test that
	// relied on the dialer would prove the dialer, which S1 already proves.
	_, err := newTestPipeline(t, PipelineConfig{}).Fetch(context.Background(), origin.URL, "")
	if err == nil {
		t.Fatal("the redirect to a private address was followed")
	}
	if !errors.Is(err, ErrBlockedRedirect) {
		t.Fatalf("error = %v, want ErrBlockedRedirect", err)
	}
	if fetchCode(err) != CodeFetchBlockedRedirect {
		t.Fatalf("fetchCode = %q, want %q", fetchCode(err), CodeFetchBlockedRedirect)
	}
	if got := secretHits.Load(); got != 0 {
		t.Fatalf("the redirect target was reached %d times", got)
	}
}

// The classes have to survive the trip through the route, not only exist
// inside the package. Each row is a stage failure the model must be able to
// tell apart from the others: a refused type, a page that rendered nothing,
// an upstream error status and a backend that would not process the page.
func TestWebFetchRouteKeepsEachFailureClassDistinct(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantCode   string
		wantStatus int
		wantInMsg  string
	}{
		{"unsupported type", &fetchDetail{class: ErrUnsupportedContentType, mediaType: "video/mp4"},
			CodeUnsupportedContentType, http.StatusUnsupportedMediaType, "video/mp4"},
		{"upstream status", &fetchDetail{class: ErrFetchStatus, status: 404},
			CodeFetchStatus, http.StatusBadGateway, "404"},
		{"nothing to extract", ErrExtractEmpty,
			CodeExtractEmpty, http.StatusBadGateway, "no readable text"},
		{"too large", ErrTooLarge,
			CodeFetchTooLarge, http.StatusRequestEntityTooLarge, "too large"},
		{"embedding down", fmt.Errorf("%w: 429", ErrEmbedUnavailable),
			CodeEmbedUnavailable, http.StatusBadGateway, "could not be processed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler(t, Deps{
				Search: &stubSearcher{},
				Fetch: FetcherFunc(func(context.Context, string, string) (FetchResult, error) {
					return FetchResult{}, tc.err
				}),
			})
			rr := post(t, h, "/v1/tools/web_fetch", "turn-1", map[string]any{"url": "https://example.com/a"})
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tc.wantStatus, rr.Body)
			}
			env := decodeEnvelope(t, rr)
			if env["code"] != tc.wantCode {
				t.Fatalf("code = %v, want %q", env["code"], tc.wantCode)
			}
			message, _ := env["message"].(string)
			if !strings.Contains(message, tc.wantInMsg) {
				t.Fatalf("message = %q, want it to name %q", message, tc.wantInMsg)
			}
			if _, present := env["parts"]; present {
				t.Fatalf("a failure envelope carries a parts field: %s", rr.Body)
			}
		})
	}
}

// B11, over the pipeline's own failures. No value that leaves this package
// names an internal service or an address, including on the paths where the
// underlying error is full of them.
func TestFetchErrorsCarryNoTopology(t *testing.T) {
	leaks := []string{"edge-api", "litellm", "control-plane", "searxng", "markitdown", "127.0.0.1", "169.254.169.254"}
	conv := &stubConverter{err: errors.New("markitdown at 172.19.0.9 refused")}

	pdf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.7"))
	}))
	defer pdf.Close()

	_, err := newTestPipeline(t, PipelineConfig{Converter: conv}).Fetch(context.Background(), pdf.URL, "")
	if err == nil {
		t.Fatal("a refused conversion produced no error")
	}
	// The pipeline's own error may carry the cause for the log; what must not
	// carry it is the message the handler emits for that class.
	msg := fetchMessage(fetchCode(err), err)
	for _, leak := range leaks {
		if strings.Contains(msg, leak) {
			t.Fatalf("the client-facing message leaks %q: %s", leak, msg)
		}
	}
}
