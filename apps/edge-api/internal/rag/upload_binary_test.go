package rag

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// spyConverter records every Convert call so tests can assert whether the
// conversion path ran and what it was handed.
type spyConverter struct {
	mu       sync.Mutex
	calls    int
	filename string
	content  string

	mdOut string
	err   error
}

func (s *spyConverter) Convert(_ context.Context, filename, _ string, _ []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.filename = filename
	return s.mdOut, s.err
}

func (s *spyConverter) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// ingestRecorder captures what IngestFunc receives.
type ingestRecorder struct {
	mu      sync.Mutex
	content string
	called  bool
}

func (rec *ingestRecorder) asIngestFunc() IngestFunc {
	return func(_ context.Context, _, _ uuid.UUID, content string) {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.content = content
		rec.called = true
	}
}

func (rec *ingestRecorder) gotContent() (string, bool) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.content, rec.called
}

// newBinaryTestHandler wires a Handler with a converter + ingest recorder,
// mirroring how main.go chains WithChat / WithEmbeddingGuard / WithConverter.
func newBinaryTestHandler(conv *spyConverter, rec *ingestRecorder) (*Handler, *[]auditRecord) {
	store := newFakeStore()
	var audits []auditRecord
	h := NewHandler(store, &fakeEmbedder{}, makeAuditCapture(&audits), rec.asIngestFunc(), context.Background())
	if conv != nil {
		h = h.WithConverter(conv, DefaultMaxUploadBytes)
	}
	return h, &audits
}

// TestHandleUpload_RawTextSkipsConverter pins the invariant that the legacy
// raw-text path never touches the converter: same 202, same ingest content,
// zero Convert calls.
func TestHandleUpload_RawTextSkipsConverter(t *testing.T) {
	conv := &spyConverter{mdOut: "SHOULD NOT APPEAR"}
	rec := &ingestRecorder{}
	h, audits := newBinaryTestHandler(conv, rec)

	body, _ := json.Marshal(UploadRequest{Name: "doc.txt", Content: "Hello world."})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleUpload(w, req)
	h.Shutdown()

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	if conv.callCount() != 0 {
		t.Errorf("converter must not run on the raw-text path, ran %d times", conv.callCount())
	}
	if content, ok := rec.gotContent(); !ok || content != "Hello world." {
		t.Errorf("ingest content = %q, want raw text untouched", content)
	}
	if len(*audits) != 1 {
		t.Errorf("expected exactly one audit event, got %d", len(*audits))
	}
}

// TestHandleUpload_Base64BinarySuccess covers the base64 shape end to end:
// bytes converted to markdown, markdown (not bytes) fed to ingest, original
// byte size recorded on the document.
func TestHandleUpload_Base64BinarySuccess(t *testing.T) {
	pdfBytes := []byte("%PDF-1.4 fake pdf payload")
	conv := &spyConverter{mdOut: "# Converted markdown"}
	rec := &ingestRecorder{}
	h, _ := newBinaryTestHandler(conv, rec)

	body, _ := json.Marshal(UploadRequest{
		Name:          "report.pdf",
		MimeType:      "application/pdf",
		ContentBase64: base64.StdEncoding.EncodeToString(pdfBytes),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleUpload(w, req)
	h.Shutdown()

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var resp DocumentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.SizeBytes != int64(len(pdfBytes)) {
		t.Errorf("size_bytes = %d, want original byte length %d", resp.SizeBytes, len(pdfBytes))
	}
	if resp.MimeType != "application/pdf" {
		t.Errorf("mime_type = %q, want application/pdf", resp.MimeType)
	}
	if conv.callCount() != 1 || conv.filename != "report.pdf" {
		t.Errorf("converter calls=%d filename=%q, want 1 call for report.pdf", conv.callCount(), conv.filename)
	}
	if content, _ := rec.gotContent(); content != "# Converted markdown" {
		t.Errorf("ingest content = %q, want converted markdown", content)
	}
}

// TestHandleUpload_OctetStreamSuccess covers the raw-binary shape: a non-JSON
// Content-Type means the request body IS the file.
func TestHandleUpload_OctetStreamSuccess(t *testing.T) {
	docxBytes := []byte("PK\x03\x04 fake docx")
	conv := &spyConverter{mdOut: "Docx body text"}
	rec := &ingestRecorder{}
	h, _ := newBinaryTestHandler(conv, rec)

	req := httptest.NewRequest(http.MethodPost,
		"/v1/rag/documents?name=notes.docx", bytes.NewReader(docxBytes))
	req.Header.Set("Content-Type",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleUpload(w, req)
	h.Shutdown()

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var resp DocumentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Name != "notes.docx" {
		t.Errorf("name = %q, want notes.docx from query param", resp.Name)
	}
	if content, _ := rec.gotContent(); content != "Docx body text" {
		t.Errorf("ingest content = %q, want converted markdown", content)
	}
}

// TestHandleUpload_ConversionFailureLoud422 proves the loud contract: a
// rejected conversion surfaces a 422 carrying the sidecar's error class and
// leaves NO document row behind.
func TestHandleUpload_ConversionFailureLoud422(t *testing.T) {
	conv := &spyConverter{err: &ConversionError{
		Rejected: true, Class: "unsupported_format", Detail: "no converter",
	}}
	rec := &ingestRecorder{}
	h, audits := newBinaryTestHandler(conv, rec)

	body, _ := json.Marshal(UploadRequest{
		Name:          "thing.pdf",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("junk")),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleUpload(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unsupported_format") {
		t.Errorf("response must carry the sidecar error class, got: %s", w.Body.String())
	}
	if conv.callCount() == 0 {
		t.Error("expected converter to be invoked")
	}
	for _, a := range *audits {
		if a.Action == "RAG_DOCUMENT_UPLOAD" {
			t.Error("upload audit must not fire for a failed conversion")
		}
	}
}

// TestHandleUpload_ConversionUnavailable503: transport-level sidecar failure
// is an infra outage (503), not a client error.
func TestHandleUpload_ConversionUnavailable503(t *testing.T) {
	conv := &spyConverter{err: &ConversionError{Rejected: false, Detail: "connection refused"}}
	rec := &ingestRecorder{}
	h, _ := newBinaryTestHandler(conv, rec)

	body, _ := json.Marshal(UploadRequest{
		Name:          "thing.pdf",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("junk")),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleUpload(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleUpload_EmptyConversionResult422: an empty markdown success from
// the sidecar is still a loud 422, never a silent empty-text embed.
func TestHandleUpload_EmptyConversionResult422(t *testing.T) {
	conv := &spyConverter{mdOut: "   \n"}
	rec := &ingestRecorder{}
	h, _ := newBinaryTestHandler(conv, rec)

	body, _ := json.Marshal(UploadRequest{
		Name:          "scan.pdf",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("%PDF")),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleUpload(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for empty conversion result, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleUpload_BinaryWithoutConverter503: unwired converter = loud 503,
// matching the WithConverter contract.
func TestHandleUpload_BinaryWithoutConverter503(t *testing.T) {
	rec := &ingestRecorder{}
	h, audits := newBinaryTestHandler(nil, rec) // no converter wired

	body, _ := json.Marshal(UploadRequest{
		Name:          "thing.pdf",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("junk")),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleUpload(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without converter, got %d", w.Code)
	}
	for _, a := range *audits {
		if a.Action == "RAG_DOCUMENT_UPLOAD" {
			t.Error("upload audit must not fire when the converter is missing")
		}
	}
}

func TestHandleUpload_Base64Invalid400(t *testing.T) {
	conv := &spyConverter{}
	rec := &ingestRecorder{}
	h, _ := newBinaryTestHandler(conv, rec)

	body, _ := json.Marshal(UploadRequest{Name: "x.pdf", ContentBase64: "!!!not-base64!!!"})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleUpload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid base64, got %d", w.Code)
	}
	if conv.callCount() != 0 {
		t.Error("converter must not run for undecodable input")
	}
}

func TestHandleUpload_BothContentAndBase64400(t *testing.T) {
	conv := &spyConverter{}
	rec := &ingestRecorder{}
	h, _ := newBinaryTestHandler(conv, rec)

	body, _ := json.Marshal(UploadRequest{
		Name:          "x.txt",
		Content:       "plain",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("bin")),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleUpload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for both fields set, got %d", w.Code)
	}
}

func TestHandleUpload_OversizeBase64413(t *testing.T) {
	conv := &spyConverter{}
	rec := &ingestRecorder{}
	h, _ := newBinaryTestHandler(conv, rec) // DefaultMaxUploadBytes cap

	big := bytes.Repeat([]byte("a"), int(DefaultMaxUploadBytes)+1024)
	body, _ := json.Marshal(UploadRequest{
		Name:          "big.bin",
		ContentBase64: base64.StdEncoding.EncodeToString(big),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleUpload(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 oversize, got %d", w.Code)
	}
	if conv.callCount() != 0 {
		t.Error("oversize payload must never reach the converter")
	}
}

func TestHandleUpload_UnsupportedExtension422(t *testing.T) {
	conv := &spyConverter{}
	rec := &ingestRecorder{}
	h, _ := newBinaryTestHandler(conv, rec)

	body, _ := json.Marshal(UploadRequest{
		Name:          "photo.png",
		MimeType:      "image/png",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("\x89PNG fake")),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleUpload(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for png upload, got %d", w.Code)
	}
	if conv.callCount() != 0 {
		t.Error("disallowed formats must be rejected before the sidecar call")
	}
}

func TestHandleUpload_OversizeBinaryBody413(t *testing.T) {
	conv := &spyConverter{}
	rec := &ingestRecorder{}
	h, _ := newBinaryTestHandler(conv, rec)

	big := bytes.Repeat([]byte("b"), int(DefaultMaxUploadBytes)+1)
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents?name=big.pdf", bytes.NewReader(big))
	req.Header.Set("Content-Type", "application/pdf")
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleUpload(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 oversize binary body, got %d", w.Code)
	}
}

// TestMarkitdownClient_Contract exercises the HTTP client against a fake
// sidecar: success passthrough, class-carrying error, and transport failure.
func TestMarkitdownClient_Contract(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"markdown":"# hello"}`))
		}))
		defer srv.Close()
		c := NewMarkitdownClient(srv.URL)
		md, err := c.Convert(context.Background(), "a.pdf", "application/pdf", []byte("x"))
		if err != nil || md != "# hello" {
			t.Fatalf("md=%q err=%v", md, err)
		}
	})

	t.Run("error carries sidecar class", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":{"code":422,"class":"unsupported_format","message":"nope"}}`))
		}))
		defer srv.Close()
		c := NewMarkitdownClient(srv.URL)
		_, err := c.Convert(context.Background(), "a.xyz", "", []byte("x"))
		var ce *ConversionError
		if !errors.As(err, &ce) {
			t.Fatalf("want ConversionError, got %v", err)
		}
	})

	t.Run("transport failure is not rejected", func(t *testing.T) {
		c := NewMarkitdownClient("http://127.0.0.1:1")
		_, err := c.Convert(context.Background(), "a.pdf", "", []byte("x"))
		var ce *ConversionError
		if !errors.As(err, &ce) {
			t.Fatalf("want ConversionError, got %v", err)
		}
	})
}

func TestExtensionAllowed(t *testing.T) {
	for _, name := range []string{"a.PDF", "b.docx", "c.pptx", "d.xlsx", "e.md"} {
		if !ExtensionAllowed(name) {
			t.Errorf("%s should be allowed", name)
		}
	}
	for _, name := range []string{"noext", "photo.png", "clip.mp4", ".zip"} {
		if ExtensionAllowed(name) {
			t.Errorf("%s should be rejected", name)
		}
	}
}

// TestHandleUpload_MismatchedNameMime400 pins the consistency rule: an
// allowed extension plus an ALLOWED but contradictory mime is a 400, not a
// silent pass-through of whichever signal happened to be allowed.
func TestHandleUpload_MismatchedNameMime400(t *testing.T) {
	conv := &spyConverter{mdOut: "should not run"}
	rec := &ingestRecorder{}
	h, _ := newBinaryTestHandler(conv, rec)

	body, _ := json.Marshal(UploadRequest{
		Name:          "notes.txt",
		MimeType:      "application/pdf",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("x")),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleUpload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for mismatched name/mime pair, got %d", w.Code)
	}
	if conv.callCount() != 0 {
		t.Error("mismatched metadata must never reach the converter")
	}
}

// TestHandleUpload_ControlCharName400: control characters in a filename are a
// client error, not a conversion outage.
func TestHandleUpload_ControlCharName400(t *testing.T) {
	conv := &spyConverter{}
	rec := &ingestRecorder{}
	h, _ := newBinaryTestHandler(conv, rec)

	body, _ := json.Marshal(UploadRequest{
		Name:          "bad\x07name.pdf",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("x")),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleUpload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for control-char filename, got %d", w.Code)
	}
}

// TestMarkitdownClient_SanitizesDetail: a hostile or buggy sidecar message
// carrying temp paths or exception class names must not reach the 422 body.
func TestMarkitdownClient_SanitizesDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"code":422,"class":"conversion_failed","message":"DocxConverter threw OSError with message: /tmp/tmpabc123/broken.docx is unreadable"}}`))
	}))
	defer srv.Close()
	c := NewMarkitdownClient(srv.URL)
	_, err := c.Convert(context.Background(), "a.docx", "", []byte("x"))
	var ce *ConversionError
	if !errors.As(err, &ce) || ce.Detail == "" {
		t.Fatalf("want ConversionError with detail, got %v", err)
	}
	sanitized := sanitizeDetail(ce.Detail)
	for _, leak := range []string{"/tmp/", ".docx", "OSError", "Exception"} {
		if strings.Contains(strings.ToLower(sanitized), strings.ToLower(leak)) {
			t.Errorf("sanitized detail leaks %q: %s", leak, sanitized)
		}
	}
}

// TestHandleUpload_OversizeResponse413: a truncated markdown response from
// the sidecar classifies as too-large, not as an outage.
func TestHandleUpload_OversizeResponse413(t *testing.T) {
	big := strings.Repeat("m", int(maxConvertResponseBytes)+10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"markdown":"` + big + `"}`))
	}))
	defer srv.Close()

	store := newFakeStore()
	var audits []auditRecord
	rec := &ingestRecorder{}
	h := NewHandler(store, &fakeEmbedder{}, makeAuditCapture(&audits), rec.asIngestFunc(), context.Background()).
		WithConverter(NewMarkitdownClient(srv.URL), DefaultMaxUploadBytes)

	body, _ := json.Marshal(UploadRequest{
		Name:          "huge.pdf",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("%PDF fake")),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleUpload(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized conversion output, got %d", w.Code)
	}
}
