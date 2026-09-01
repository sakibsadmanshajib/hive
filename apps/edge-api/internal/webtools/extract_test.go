package webtools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/rag"
)

// countingReader reports how many bytes were actually read from it. Criterion
// B6 is written as "asserted by requiring zero bytes read from the body
// reader", so the assertion needs a reader that can answer that question
// rather than a byte slice that cannot.
type countingReader struct {
	src  *strings.Reader
	read int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.src.Read(p)
	c.read += n
	return n, err
}

func newCountingReader(s string) *countingReader {
	return &countingReader{src: strings.NewReader(s)}
}

type stubConverter struct {
	calls    int
	markdown string
	err      error
	gotName  string
	gotType  string
	gotBytes int
}

func (s *stubConverter) Convert(_ context.Context, filename, contentType string, data []byte) (string, error) {
	s.calls++
	s.gotName = filename
	s.gotType = contentType
	s.gotBytes = len(data)
	return s.markdown, s.err
}

func testExtractor(conv rag.Converter) Extractor {
	return Extractor{Converter: conv, MaxBytes: MaxFetchBytes}
}

// B6. A response of an unlisted content type is refused before the body is
// read. Not "before the body is used": before a single byte leaves the
// reader, because the reader is the network and reading it is the cost.
func TestExtractRefusesUnlistedContentTypesWithoutReadingTheBody(t *testing.T) {
	for _, ct := range []string{
		"image/png",
		"image/jpeg",
		"video/mp4",
		"audio/mpeg",
		"application/octet-stream",
		"application/zip",
		"application/x-msdownload",
		"",
	} {
		t.Run(ct, func(t *testing.T) {
			body := newCountingReader(strings.Repeat("x", 4096))
			conv := &stubConverter{markdown: "never"}
			_, err := testExtractor(conv).Extract(context.Background(), ct, "/a", body)
			if !errors.Is(err, ErrUnsupportedContentType) {
				t.Fatalf("error = %v, want ErrUnsupportedContentType", err)
			}
			if body.read != 0 {
				t.Fatalf("%d bytes were read from the body of a refused content type; want 0", body.read)
			}
			if conv.calls != 0 {
				t.Fatalf("the converter was called %d times for a refused content type", conv.calls)
			}
		})
	}
}

// B5, half of it. The byte cap is enforced during the read on the text
// branch: a body past the cap is refused, and the refusal does not depend on
// a Content-Length header the server controls.
func TestExtractRefusesOversizedTextBodies(t *testing.T) {
	e := Extractor{Converter: &stubConverter{}, MaxBytes: 1024}
	body := newCountingReader("<html><body>" + strings.Repeat("a", 4096) + "</body></html>")
	_, err := e.Extract(context.Background(), "text/html; charset=utf-8", "/a", body)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("error = %v, want ErrTooLarge", err)
	}
	// One byte past the cap is enough to decide. Anything materially beyond
	// that means the whole body was buffered first, which is the shape of
	// issue #1638 (retrieval/utils.py:170 reads response.content whole).
	if int64(body.read) > e.MaxBytes+512 {
		t.Fatalf("read %d bytes to enforce a %d byte cap; the cap is not enforced during the read", body.read, e.MaxBytes)
	}
}

// B5, the other half, and the branch issue #1638 is actually about: a PDF
// goes down the binary branch, which is the one the fork reads unbounded.
func TestExtractRefusesOversizedBinaryBodies(t *testing.T) {
	conv := &stubConverter{markdown: "converted"}
	e := Extractor{Converter: conv, MaxBytes: 1024}
	body := newCountingReader(strings.Repeat("%PDF-1.7\n", 1024))
	_, err := e.Extract(context.Background(), "application/pdf", "/report.pdf", body)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("error = %v, want ErrTooLarge", err)
	}
	if conv.calls != 0 {
		t.Fatalf("an oversized document reached the converter %d times", conv.calls)
	}
	if int64(body.read) > e.MaxBytes+512 {
		t.Fatalf("read %d bytes to enforce a %d byte cap on the binary branch", body.read, e.MaxBytes)
	}
}

func TestExtractHTMLKeepsTextAndDropsScriptStyleAndMarkup(t *testing.T) {
	const page = `<!DOCTYPE html>
<html><head><title>  Hive  docs </title>
<style>body { color: red; }</style>
<script>var leak = "SCRIPTBODY";</script>
</head>
<body>
<nav>Home</nav>
<h1>Rates &amp; limits</h1>
<p>The gateway meters <b>every</b> call.</p>
<!-- COMMENTBODY -->
<noscript>NOSCRIPTBODY</noscript>
<svg><text>SVGBODY</text></svg>
</body></html>`
	doc, err := testExtractor(nil).Extract(context.Background(), "text/html", "/docs", strings.NewReader(page))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if doc.Title != "Hive docs" {
		t.Fatalf("title = %q, want %q", doc.Title, "Hive docs")
	}
	for _, banned := range []string{"SCRIPTBODY", "COMMENTBODY", "NOSCRIPTBODY", "SVGBODY", "color: red", "<p>", "<b>"} {
		if strings.Contains(doc.Text, banned) {
			t.Fatalf("extracted text carries %q: %q", banned, doc.Text)
		}
	}
	if !strings.Contains(doc.Text, "Rates & limits") {
		t.Fatalf("entities were not decoded: %q", doc.Text)
	}
	if !strings.Contains(doc.Text, "The gateway meters every call.") {
		t.Fatalf("inline markup was not joined into readable text: %q", doc.Text)
	}
}

// B9. A page that parses fine and yields no text is extract_empty, and it is
// a different value from every neighbouring failure. This is the JavaScript
// only page, and it is the case most likely to be misread as the model
// failing rather than the fetch succeeding and the extraction not.
func TestExtractEmptyIsItsOwnClass(t *testing.T) {
	doc, err := testExtractor(nil).Extract(context.Background(), "text/html",
		"/app", strings.NewReader(`<html><body><script>render()</script></body></html>`))
	if !errors.Is(err, ErrExtractEmpty) {
		t.Fatalf("error = %v (doc %+v), want ErrExtractEmpty", err, doc)
	}
	codes := map[string]bool{}
	for _, e := range []error{ErrExtractEmpty, ErrExtractFailed, ErrUnsupportedContentType, ErrFetchStatus, ErrTooLarge, ErrEmbedUnavailable, ErrReduceEmpty} {
		code := fetchCode(e)
		if codes[code] {
			t.Fatalf("two failure classes collapse to the same envelope code %q", code)
		}
		codes[code] = true
	}
}

// B10, the extraction half. Zero width and bidi characters are the standard
// way to hide an instruction from whoever reviews the text while leaving it
// legible to the model reading it.
func TestExtractStripsInvisibleCharacters(t *testing.T) {
	// Written as escapes on purpose: a literal zero-width character in a
	// source file is invisible to the next reader, which is the whole reason
	// these are worth stripping.
	page := "<html><body><p>visible\u200btext\u202e and \ufeff\u2066more</p></body></html>"
	doc, err := testExtractor(nil).Extract(context.Background(), "text/html", "/a", strings.NewReader(page))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	for _, r := range doc.Text {
		if r == '\ufeff' || (r >= '\u200b' && r <= '\u200f') ||
			(r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069') {
			t.Fatalf("invisible character %U survived extraction: %q", r, doc.Text)
		}
	}
	if !strings.Contains(doc.Text, "visibletext and more") {
		t.Fatalf("stripping mangled the legible text: %q", doc.Text)
	}
}

func TestExtractRoutesDocumentsToTheConverter(t *testing.T) {
	for _, tc := range []struct {
		contentType string
		path        string
		wantName    string
	}{
		{"application/pdf", "/files/rates.pdf", "rates.pdf"},
		{"application/pdf", "/download?id=9", "document.pdf"},
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "/a/plan.docx", "plan.docx"},
		{"application/epub+zip", "/book", "document.epub"},
	} {
		t.Run(tc.contentType+tc.path, func(t *testing.T) {
			conv := &stubConverter{markdown: "# Rates\n\nOne credit per million tokens."}
			doc, err := testExtractor(conv).Extract(context.Background(), tc.contentType, tc.path, strings.NewReader("BINARY"))
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if conv.calls != 1 {
				t.Fatalf("converter called %d times, want 1", conv.calls)
			}
			if conv.gotName != tc.wantName {
				t.Fatalf("converter filename hint = %q, want %q", conv.gotName, tc.wantName)
			}
			if conv.gotBytes != len("BINARY") {
				t.Fatalf("converter received %d bytes, want %d", conv.gotBytes, len("BINARY"))
			}
			if !strings.Contains(doc.Text, "One credit per million tokens.") {
				t.Fatalf("converted text = %q", doc.Text)
			}
		})
	}
}

func TestExtractConverterFailureIsExtractFailed(t *testing.T) {
	conv := &stubConverter{err: &rag.ConversionError{Rejected: true, Class: "conversion_failed", Detail: "markitdown at 172.19.0.9 said no"}}
	_, err := testExtractor(conv).Extract(context.Background(), "application/pdf", "/a.pdf", strings.NewReader("BINARY"))
	if !errors.Is(err, ErrExtractFailed) {
		t.Fatalf("error = %v, want ErrExtractFailed", err)
	}
}

// A document content type with no converter wired is a failure to extract,
// never an unsupported type: the type is supported, the deployment is not
// carrying the piece that reads it, and telling the model "that is not a
// document" would be false.
func TestExtractWithoutAConverterIsExtractFailed(t *testing.T) {
	_, err := testExtractor(nil).Extract(context.Background(), "application/pdf", "/a.pdf", strings.NewReader("BINARY"))
	if !errors.Is(err, ErrExtractFailed) {
		t.Fatalf("error = %v, want ErrExtractFailed", err)
	}
}

func TestExtractPlainTextBranches(t *testing.T) {
	for _, ct := range []string{"text/plain", "text/markdown", "application/json", "application/xml", "text/xml", "application/xhtml+xml"} {
		t.Run(ct, func(t *testing.T) {
			doc, err := testExtractor(nil).Extract(context.Background(), ct+"; charset=utf-8", "/a", strings.NewReader("hello there"))
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if !strings.Contains(doc.Text, "hello there") {
				t.Fatalf("text = %q", doc.Text)
			}
		})
	}
}
