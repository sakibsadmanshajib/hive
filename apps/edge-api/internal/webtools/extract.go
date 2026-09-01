package webtools

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/rag"
)

// Stages 2 and 3 of the web_fetch pipeline: dispatch on content type, then
// extract readable text.
//
// Two rules shape this file, and both are about what happens before a byte is
// read rather than after.
//
// The content-type allowlist decides the branch first, so an image, a video
// or an opaque binary is refused without the body ever being pulled off the
// socket (criterion B6). Reading it and then deciding is the same cost as not
// having a check.
//
// The byte cap is enforced during the read, with io.LimitReader, on BOTH
// branches. That is the gap issue #1638 names: the fork caps only its aiohttp
// text branch (apply_web_loader_bounds_patch.py), while
// retrieval/utils.py:170 reads response.content whole on the binary branch,
// which is the branch a PDF takes. One LimitReader here covers both, with no
// patch to keep in sync.

const (
	// MaxFetchBytes caps one fetched response, both branches, enforced during
	// the read. 10 MiB matches the ceiling the fork's own web loader patch
	// applies to its text branch, so the two paths do not disagree while both
	// exist.
	//
	// ponytail: a constant, not an environment variable. Nothing has asked to
	// change it, and the fork-side knob this mirrors (web.fetch.max_content_length)
	// is a post-extraction character truncation rather than a read bound, so
	// there is nothing to keep in sync. Upgrade path if an operator ever needs
	// it: PipelineConfig.MaxBytes already carries it, so only the wiring in
	// main.go would change.
	MaxFetchBytes int64 = 10 << 20
)

var (
	// ErrTooLarge is the byte cap refusal, criterion B5.
	ErrTooLarge = errors.New("webtools: response exceeds the byte cap")
	// ErrUnsupportedContentType is the stage 2 refusal, criterion B6.
	ErrUnsupportedContentType = errors.New("webtools: unsupported content type")
	// ErrExtractFailed is a parse or conversion failure: the bytes arrived
	// and could not be turned into text.
	ErrExtractFailed = errors.New("webtools: could not extract text")
	// ErrExtractEmpty is a successful parse that produced no text, which is
	// what a JavaScript-only page does. Distinct from ErrExtractFailed on
	// purpose (criterion B9): the model is told the fetch succeeded and the
	// extraction did not, rather than being handed silence it will read as
	// its own failure.
	ErrExtractEmpty = errors.New("webtools: page has no readable text")
)

// textMediaTypes go to the text extractor. Everything here is something a
// model can read once the markup is off it.
var textMediaTypes = map[string]bool{
	"text/html":             true,
	"application/xhtml+xml": true,
	"text/plain":            true,
	"text/markdown":         true,
	"text/x-markdown":       true,
	"text/csv":              true,
	"application/json":      true,
	"application/ld+json":   true,
	"application/xml":       true,
	"text/xml":              true,
}

// htmlMediaTypes are the subset of textMediaTypes that carry markup worth
// stripping rather than text worth keeping verbatim.
var htmlMediaTypes = map[string]bool{
	"text/html":             true,
	"application/xhtml+xml": true,
}

// docMediaTypes are the binary document types that go to the markitdown
// sidecar, mapped to the extension the sidecar wants as its filename hint.
//
// The map is the mime-to-extension bridge only. What actually decides whether
// a document is admissible is rag.ExtensionAllowed, the same allowlist the
// document ingest path already uses, asked below. Two lists that could
// disagree would be one list too many.
var docMediaTypes = map[string]string{
	"application/pdf":               ".pdf",
	"application/rtf":               ".rtf",
	"text/rtf":                      ".rtf",
	"application/epub+zip":          ".epub",
	"application/msword":            ".doc",
	"application/vnd.ms-excel":      ".xls",
	"application/vnd.ms-powerpoint": ".ppt",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   ".docx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         ".xlsx",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": ".pptx",
}

// mediaTypePattern is what a media type may look like before it is allowed
// into a client-facing message. The type comes off an upstream response
// header, so it is attacker-influenceable text, and this is the shape check
// that keeps it from being anything but a media type.
var mediaTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{0,48}/[a-z0-9][a-z0-9.+-]{0,48}$`)

// Doc is extracted page content, before reduction.
type Doc struct {
	Text  string
	Title string
	// MediaType is the normalised type the content was read as. Reported for
	// logs and for the unsupported-type message; never a raw header value.
	MediaType string
}

// Extractor turns a response body into a Doc.
type Extractor struct {
	// Converter is the markitdown sidecar, used only on the binary branch. A
	// nil Converter makes a document type an extract failure rather than an
	// unsupported one: the type is supported, this deployment is not carrying
	// the piece that reads it, and telling the model "that is not a document"
	// would be false.
	Converter rag.Converter
	// MaxBytes is the read cap. Zero means MaxFetchBytes.
	MaxBytes int64
}

// Extract dispatches on content type and returns the extracted text.
//
// urlPath is the request path, used only to derive a filename hint for the
// converter. It never decides admissibility: the header decides, so a
// server cannot get a body past the allowlist by naming it .pdf.
func (e Extractor) Extract(ctx context.Context, contentType, urlPath string, body io.Reader) (Doc, error) {
	mediaType := normaliseMediaType(contentType)

	switch {
	case textMediaTypes[mediaType]:
		data, err := readCapped(body, e.max())
		if err != nil {
			return Doc{}, err
		}
		return e.extractText(mediaType, string(data))

	case docMediaTypes[mediaType] != "":
		name := documentName(urlPath, docMediaTypes[mediaType])
		// The one gate on the binary branch is the ingest path's own
		// allowlist, asked rather than copied.
		if !rag.ExtensionAllowed(name) {
			return Doc{}, unsupportedType(mediaType)
		}
		if e.Converter == nil {
			return Doc{}, fmt.Errorf("%w: no converter is wired", ErrExtractFailed)
		}
		// Read capped BEFORE the converter is called, so an oversized
		// document is refused here rather than forwarded to the sidecar.
		data, err := readCapped(body, e.max())
		if err != nil {
			return Doc{}, err
		}
		markdown, err := e.Converter.Convert(ctx, name, mediaType, data)
		if err != nil {
			return Doc{}, fmt.Errorf("%w: %w", ErrExtractFailed, err)
		}
		return finishDoc(Doc{Text: markdown, MediaType: mediaType, Title: strings.TrimSuffix(name, path.Ext(name))})

	default:
		// Refused before a single byte is read from body. This is the whole
		// of criterion B6, and it is why the read calls are inside the two
		// branches above rather than above the switch.
		return Doc{}, unsupportedType(mediaType)
	}
}

func (e Extractor) max() int64 {
	if e.MaxBytes > 0 {
		return e.MaxBytes
	}
	return MaxFetchBytes
}

func (e Extractor) extractText(mediaType, raw string) (Doc, error) {
	if htmlMediaTypes[mediaType] {
		title, text := htmlToText(raw)
		return finishDoc(Doc{Text: text, Title: title, MediaType: mediaType})
	}
	return finishDoc(Doc{Text: raw, MediaType: mediaType})
}

// finishDoc applies the two rules every branch shares: invisible characters
// are stripped (criterion B10) and an empty result is a loud ErrExtractEmpty
// rather than a Doc nobody looks inside.
func finishDoc(d Doc) (Doc, error) {
	d.Text = collapseWhitespace(stripInvisible(d.Text))
	d.Title = truncateRunes(collapseWhitespace(stripInvisible(d.Title)), 300)
	if d.Text == "" {
		return Doc{}, ErrExtractEmpty
	}
	return d, nil
}

// readCapped reads at most max bytes and refuses anything past it. It reads
// one byte beyond the cap so an exact fit is distinguishable from a body that
// was cut short, and it never buffers more than that, which is the property
// issue #1638's response.content read does not have.
func readCapped(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if int64(len(data)) > max {
		return nil, ErrTooLarge
	}
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	return data, nil
}

// normaliseMediaType reduces a Content-Type header to a bare lowercase media
// type. An absent or unparseable header is deliberately NOT treated as HTML
// (which is what the fork's _is_text_content_type does): a server that will
// not say what it sent does not get to have it guessed.
func normaliseMediaType(contentType string) string {
	parsed, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		// Fall back to the part before the first ";" so a malformed
		// parameter list does not lose an otherwise usable type.
		bare := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
		if mediaTypePattern.MatchString(bare) {
			return bare
		}
		return ""
	}
	return strings.ToLower(parsed)
}

// unsupportedType builds the stage 2 refusal, carrying the media type so the
// model can be told what the link actually was rather than only that it
// failed. The type is shape-checked before it is carried: it comes off an
// upstream header, and an unchecked one would be arbitrary attacker text on
// its way into a client-facing message.
func unsupportedType(mediaType string) error {
	if !mediaTypePattern.MatchString(mediaType) {
		return ErrUnsupportedContentType
	}
	return &fetchDetail{class: ErrUnsupportedContentType, mediaType: mediaType}
}

// documentName derives the filename hint the converter is given. The URL's
// own basename is used when it already carries the right extension, since a
// real name helps the sidecar and helps the log; otherwise a synthetic one.
func documentName(urlPath, ext string) string {
	base := path.Base(urlPath)
	if idx := strings.IndexAny(base, "?#"); idx >= 0 {
		base = base[:idx]
	}
	if unescaped, err := url.PathUnescape(base); err == nil {
		base = unescaped
	}
	base = strings.TrimSpace(base)
	if base != "" && base != "." && base != "/" &&
		strings.EqualFold(path.Ext(base), ext) &&
		!strings.ContainsAny(base, `/\`) {
		return base
	}
	return "document" + ext
}

// skipElements have their contents dropped entirely rather than turned into
// text. Without this the "extracted text" of an ordinary page is a soup of
// inline JavaScript and CSS, which costs context and teaches the model
// nothing.
var skipElements = map[string]bool{
	"script": true, "style": true, "noscript": true,
	"svg": true, "template": true, "iframe": true, "canvas": true,
}

// blockElements end a line, so paragraphs and list items do not run together
// into one sentence that says something neither of them said.
var blockElements = map[string]bool{
	"p": true, "div": true, "br": true, "li": true, "tr": true, "td": true, "th": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"section": true, "article": true, "header": true, "footer": true, "nav": true,
	"blockquote": true, "pre": true, "ul": true, "ol": true, "table": true, "hr": true,
}

// htmlToText returns the document title and the readable text of an HTML
// document.
//
// ponytail: a tag scanner, not a DOM parser. golang.org/x/net/html is not a
// dependency of this module and this needs neither a tree nor a readability
// score: script, style and comment contents are dropped, block elements
// become line breaks, entities are decoded, and the rest is text. The known
// ceiling is that it does not identify a main article region, so a page's
// navigation text is extracted alongside its body, and that it assumes UTF-8
// rather than honouring a charset parameter. Upgrade path, if the extracted
// text is ever measured to be too noisy to rank well: route text/html to the
// markitdown sidecar, which already accepts it, at the cost of a network hop
// on the most common branch.
func htmlToText(src string) (string, string) {
	var body, title strings.Builder
	var skipping string
	inTitle := false

	emit := func(s string) {
		if skipping != "" {
			return
		}
		if inTitle {
			title.WriteString(s)
			return
		}
		body.WriteString(s)
	}

	for i := 0; i < len(src); {
		lt := strings.IndexByte(src[i:], '<')
		if lt < 0 {
			emit(src[i:])
			break
		}
		emit(src[i : i+lt])
		i += lt

		if strings.HasPrefix(src[i:], "<!--") {
			end := strings.Index(src[i+4:], "-->")
			if end < 0 {
				break
			}
			i += 4 + end + 3
			continue
		}

		gt := strings.IndexByte(src[i:], '>')
		if gt < 0 {
			break
		}
		raw := src[i+1 : i+gt]
		i += gt + 1

		name, closing := tagName(raw)
		if name == "" {
			continue
		}
		if skipping != "" {
			if closing && name == skipping {
				skipping = ""
			}
			continue
		}
		switch {
		case closing && name == "title":
			inTitle = false
		case closing:
			if blockElements[name] {
				body.WriteByte('\n')
			}
		case skipElements[name] && !strings.HasSuffix(raw, "/"):
			skipping = name
		case name == "title":
			inTitle = true
		case blockElements[name]:
			body.WriteByte('\n')
		}
	}

	return html.UnescapeString(title.String()), html.UnescapeString(body.String())
}

// tagName returns the lowercase element name of a raw tag body and whether it
// was a closing tag. Doctypes and processing instructions return "".
func tagName(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "!") || strings.HasPrefix(raw, "?") {
		return "", false
	}
	closing := strings.HasPrefix(raw, "/")
	raw = strings.TrimPrefix(raw, "/")
	end := strings.IndexAny(raw, " \t\r\n/>")
	if end >= 0 {
		raw = raw[:end]
	}
	return strings.ToLower(strings.TrimSpace(raw)), closing
}

// collapseWhitespace turns extracted text into something a model reads as
// prose: runs of spaces become one space, runs of blank lines become one, and
// each line is trimmed. Without it an HTML page arrives as mostly indentation.
func collapseWhitespace(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimSpace(strings.Join(strings.Fields(line), " "))
		if line == "" {
			if blank || len(out) == 0 {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
