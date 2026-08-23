package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// Converter turns binary document bytes into markdown text for RAG ingest.
type Converter interface {
	Convert(ctx context.Context, filename, contentType string, data []byte) (string, error)
}

// ConversionError distinguishes "the sidecar refused this input" (loud 422,
// client fixable) from "the sidecar could not be reached" (loud 503). Class
// and Detail carry the sidecar's error class and message verbatim so the
// caller's error surfaces the converter's reason, not a generic failure.
type ConversionError struct {
	Rejected bool   // true: sidecar answered with an error response
	Class    string // sidecar error class (unsupported_format, conversion_failed, empty_result, ...)
	Detail   string // sidecar error message
}

func (e *ConversionError) Error() string {
	if e.Rejected {
		return fmt.Sprintf("document conversion rejected (%s): %s", e.Class, e.Detail)
	}
	return "conversion service unreachable"
}

// allowedExtensions bounds what the binary ingest path forwards to the
// markitdown sidecar. Generous across document formats, bounded away from
// binaries whose only conversion outcome is mojibake: markitdown's plain-text
// fallback echoes arbitrary bytes as text, so images and opaque binaries are
// rejected here before the sidecar is ever called.
var allowedExtensions = map[string]bool{
	".pdf":  true,
	".docx": true,
	".doc":  true,
	".pptx": true,
	".ppt":  true,
	".xlsx": true,
	".xls":  true,
	".csv":  true,
	".json": true,
	".xml":  true,
	".html": true,
	".htm":  true,
	".md":   true,
	".txt":  true,
	".rtf":  true,
	".epub": true,
}

// ExtensionAllowed reports whether filename carries an extension this ingest
// path accepts. Empty extension is never allowed on the binary path.
func ExtensionAllowed(filename string) bool {
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(filename)))
	return allowedExtensions[ext]
}

// allowedContentTypes mirrors the extension allowlist for uploads whose name
// carries no usable extension. Same boundary rule: generous across document
// formats, closed to binaries markitdown can only echo as mojibake.
var allowedContentTypes = map[string]bool{
	"application/pdf":      true,
	"application/msword":   true,
	"application/rtf":      true,
	"application/xml":      true,
	"application/json":     true,
	"application/epub+zip": true,
	"text/html":            true,
	"text/plain":           true,
	"text/csv":             true,
	"text/markdown":        true,
	"text/xml":             true,
	// OOXML formats
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   true,
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": true,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         true,
	// Legacy OLE office formats
	"application/vnd.ms-powerpoint": true,
	"application/vnd.ms-excel":      true,
}

// MarkitdownClient is the Converter implementation that talks to the pinned
// markitdown sidecar over the compose network. POST {base}/convert with the
// raw bytes as the body, X-Filename and Content-Type as hints.
type MarkitdownClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewMarkitdownClient builds a client for the sidecar base URL (no trailing
// slash, no /v1 suffix).
func NewMarkitdownClient(baseURL string) *MarkitdownClient {
	return &MarkitdownClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		// Generous: a 25MB PPTX can take tens of seconds on the shared box.
		httpClient: &http.Client{Timeout: 180 * time.Second},
	}
}

type markitdownSuccess struct {
	Markdown string `json:"markdown"`
}

type markitdownErrorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Class   string `json:"class"`
		Message string `json:"message"`
	} `json:"error"`
}

// Convert posts the document bytes to the sidecar and returns the markdown
// text. Error contract:
//   - sidecar answered with any non-2xx JSON error -> ConversionError with
//     Rejected=true, carrying its class + message
//   - empty markdown on a 2xx -> ConversionError Rejected=true, class
//     empty_result (defense in depth; the sidecar already fails loud)
//   - transport failure or unreadable response -> ConversionError with
//     Rejected=false
func (c *MarkitdownClient) Convert(ctx context.Context, filename, contentType string, data []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/convert", bytes.NewReader(data))
	if err != nil {
		return "", &ConversionError{Rejected: false, Class: "client_error", Detail: err.Error()}
	}
	if filename != "" {
		req.Header.Set("X-Filename", filename)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	} else {
		req.Header.Set("Content-Type", "application/octet-stream")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", &ConversionError{Rejected: false, Detail: err.Error()}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxConvertResponseBytes))
	if err != nil {
		return "", &ConversionError{Rejected: false, Detail: err.Error()}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var eb markitdownErrorBody
		if json.Unmarshal(body, &eb) == nil && eb.Error.Class != "" {
			return "", &ConversionError{Rejected: true, Class: eb.Error.Class, Detail: eb.Error.Message}
		}
		return "", &ConversionError{
			Rejected: true, Class: "conversion_failed",
			Detail: fmt.Sprintf("sidecar returned status %d", resp.StatusCode),
		}
	}

	var ok markitdownSuccess
	if err := json.Unmarshal(body, &ok); err != nil {
		return "", &ConversionError{
			Rejected: false, Class: "bad_response",
			Detail: "sidecar success response was not valid JSON",
		}
	}
	if strings.TrimSpace(ok.Markdown) == "" {
		// Loud failure contract: an empty conversion result is a failure,
		// never an empty-text success.
		return "", &ConversionError{Rejected: true, Class: "empty_result", Detail: "converter produced no text"}
	}
	return ok.Markdown, nil
}

// maxConvertResponseBytes caps the markdown response the client will read
// back. A 25MB document converts to at most a few MB of text; anything
// beyond this is a broken sidecar, not a real conversion.
const maxConvertResponseBytes = 64 * 1024 * 1024
