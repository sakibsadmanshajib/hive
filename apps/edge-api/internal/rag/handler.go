package rag

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
)

const maxTopK = 100

// DefaultMaxUploadBytes caps the raw byte size of a document accepted on the
// binary ingest path. Matches the sidecar's own default cap: markitdown loads
// whole files into memory, so the ceiling exists on both hops.
const DefaultMaxUploadBytes = 25 * 1024 * 1024

// maxRawTextBytes preserves the historical 10MB ceiling on the raw-text JSON
// upload path so raising the reader limit for base64 payloads does not also
// loosen the legacy text constraint.
const maxRawTextBytes = 10 * 1024 * 1024

// AuditFunc emits a durable audit event. main.go wires the real
// chat.InsertAuditEvent; tests inject a recorder.
// action, resourceType, resourceID, severity, actorID, tenantID, after.
type AuditFunc func(ctx context.Context, action, resourceType, resourceID, severity string,
	tenantID, actorID uuid.UUID, userAgent string, after any)

// IngestFunc chunks, embeds, and stores a document asynchronously.
// tenantID + docID are passed so the worker can scope its DB writes.
type IngestFunc func(ctx context.Context, tenantID, docID uuid.UUID, content string)

// Store is the minimal interface the handler needs from Repo.
// Exported so main.go can declare a typed nil when the DB pool is absent.
type Store interface {
	InsertDocument(ctx context.Context, tenantID uuid.UUID, name, mimeType string, sizeBytes int64) (uuid.UUID, error)
	GetDocument(ctx context.Context, tenantID, docID uuid.UUID) (DocRow, error)
	ListDocuments(ctx context.Context, tenantID uuid.UUID) ([]DocRow, error)
	// DeleteDocument deletes the document and reports whether a row was found.
	// found=false means no row matched (caller should 404); error means infra failure.
	DeleteDocument(ctx context.Context, tenantID, docID uuid.UUID) (found bool, err error)
	SearchChunks(ctx context.Context, tenantID uuid.UUID, queryVec []float32, topK int, projectID uuid.UUID) ([]ChunkRow, error)
	// EmbeddingMismatch reports whether tenantID has embedded documents whose
	// stored provenance differs from the model/dim passed in. See
	// WithEmbeddingGuard for how the handler uses this.
	EmbeddingMismatch(ctx context.Context, tenantID uuid.UUID, model string, dim int) (bool, error)

	// Projects (issue #1595). GetProject is the ownership oracle every project
	// scoped path consults before it filters by a client supplied project id;
	// see Handler.authorizeProject.
	GetProject(ctx context.Context, tenantID, projectID uuid.UUID) (ProjectRow, error)
	CreateProject(ctx context.Context, tenantID, ownerUserID uuid.UUID, name, instructions string) (ProjectRow, error)
	ListProjects(ctx context.Context, tenantID, ownerUserID uuid.UUID) ([]ProjectRow, error)
	UpdateProject(ctx context.Context, tenantID, ownerUserID, projectID uuid.UUID, name, instructions *string) (ProjectRow, error)
	DeleteProject(ctx context.Context, tenantID, ownerUserID, projectID uuid.UUID) error
	AttachDocument(ctx context.Context, tenantID, ownerUserID, projectID, docID uuid.UUID) error
}

// store aliases Store for backward-compat with existing unexported references.
type store = Store

// Handler serves /v1/rag/* routes.
type Handler struct {
	store       store
	embed       Embedder
	audit       AuditFunc
	ingest      IngestFunc
	serverCtx   context.Context  // root context for background goroutines
	wg          sync.WaitGroup   // tracks in-flight ingest goroutines
	selectRoute RouteSelectFunc  // nil until WithChat is called; POST /v1/rag/chat returns 503
	dispatch    ChatDispatchFunc // nil until WithChat is called; POST /v1/rag/chat returns 503
	converter   Converter        // nil until WithConverter is called; binary uploads return 503

	// accounting and billing are the money path for POST /v1/rag/chat, wired
	// by WithBilling. Unlike the optional dependencies above, these are NOT
	// optional in effect: a handler without them refuses every grounded chat
	// request rather than serving inference it cannot charge for, which is the
	// #669 behaviour and the one thing this must not silently fall back to.
	accounting *inference.AccountingClient
	billing    BillingResolver

	// maxUploadBytes caps the binary/base64 upload path; see WithConverter.
	maxUploadBytes int64

	// guardEnabled/guardModel/guardDim back the embedding-consistency guard;
	// see WithEmbeddingGuard. Disabled by default so existing NewHandler
	// call sites (and their tests) are unaffected.
	guardEnabled bool
	guardModel   string
	guardDim     int
}

// WithEmbeddingGuard enables the fail-closed embedding-consistency guard on
// this Handler and returns it for chaining. Once enabled, every
// /v1/rag/search and /v1/rag/chat request first asks the store whether the
// caller's tenant has any embedded document whose stored provenance
// (embedding_model, embedding_dim) differs from model/dim; a mismatch fails
// the request closed with a clear error instead of comparing today's query
// vector against chunks from a different embedding space. Wired in main.go
// with the same EMBEDDING_MODEL/EmbeddingDimension the process is configured
// with; unwired (default) Handlers never check.
func (h *Handler) WithEmbeddingGuard(model string, dim int) *Handler {
	h.guardModel = model
	h.guardDim = dim
	h.guardEnabled = true
	return h
}

// WithConverter wires the binary-document conversion path (pinned markitdown
// sidecar) onto this Handler and returns it for chaining. Once wired, POST
// /v1/rag/documents accepts a raw file body with a non-JSON Content-Type, or
// a JSON body with content_base64, converts the bytes to markdown through the
// sidecar, and feeds the result into the existing chunk + embed pipeline
// untouched. maxUploadBytes <= 0 falls back to DefaultMaxUploadBytes.
// Unwired (default) Handlers reject binary uploads with a loud 503.
func (h *Handler) WithConverter(c Converter, maxUploadBytes int64) *Handler {
	h.converter = c
	if maxUploadBytes <= 0 {
		maxUploadBytes = DefaultMaxUploadBytes
	}
	h.maxUploadBytes = maxUploadBytes
	return h
}

// checkEmbeddingGuard reports whether the request should proceed. A store
// error is logged and treated as pass-through -- the SearchChunks call that
// follows would surface the same infra failure as its own 500, so this does
// not invent a second failure mode for a transient DB hiccup; only a
// confirmed mismatch fails the request closed.
func (h *Handler) checkEmbeddingGuard(ctx context.Context, tenantID uuid.UUID) bool {
	if !h.guardEnabled {
		return true
	}
	mismatch, err := h.store.EmbeddingMismatch(ctx, tenantID, h.guardModel, h.guardDim)
	if err != nil {
		log.Printf("rag: embedding guard check failed, allowing request through: %v", err)
		return true
	}
	if mismatch {
		log.Printf("rag: embedding guard: tenant %s has documents embedded under a different model/dim than the current configuration (model=%s dim=%d); failing closed", tenantID, h.guardModel, h.guardDim)
		return false
	}
	return true
}

// NewHandler constructs a Handler.
// serverCtx must be the process-level context so ingest goroutines respect shutdown.
func NewHandler(s store, embed Embedder, audit AuditFunc, ingest IngestFunc, serverCtx context.Context) *Handler {
	if serverCtx == nil {
		serverCtx = context.Background()
	}
	h := &Handler{store: s, embed: embed, audit: audit, ingest: ingest, serverCtx: serverCtx}
	// The upload cap applies even before WithConverter runs so an unwired
	// handler still bounds payloads instead of treating 0 as unlimited.
	h.maxUploadBytes = DefaultMaxUploadBytes
	return h
}

// Register mounts all /v1/rag/* routes on mux.
// Callers wrap with featuregate.Require(FeatureRAG) before mounting.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/rag/documents", h.routeDocuments)
	mux.HandleFunc("/v1/rag/documents/", h.routeDocumentByID)
	mux.HandleFunc("/v1/rag/search", h.handleSearch)
	mux.HandleFunc("/v1/rag/chat", h.handleChat)
	mux.HandleFunc("/v1/rag/projects", h.routeProjects)
	mux.HandleFunc("/v1/rag/projects/", h.routeProjectByID)
}

// Shutdown waits for in-flight ingest goroutines to finish. Call on server shutdown.
func (h *Handler) Shutdown() { h.wg.Wait() }

func (h *Handler) routeDocuments(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.handleUpload(w, r)
	case http.MethodGet:
		h.handleList(w, r)
	default:
		apierrors.Write(w, http.StatusMethodNotAllowed, apierrors.CodeInvalidRequest, "method not allowed")
	}
}

func (h *Handler) routeDocumentByID(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleGetDocument(w, r)
	case http.MethodDelete:
		h.handleDelete(w, r)
	default:
		apierrors.Write(w, http.StatusMethodNotAllowed, apierrors.CodeInvalidRequest, "method not allowed")
	}
}

// uploadPayload is the normalized result of any accepted upload shape.
type uploadPayload struct {
	name      string
	mimeType  string
	content   string // text fed to ingest: raw text as-is, or converted markdown
	sizeBytes int64  // size of the source document (raw bytes when binary)
}

func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}

	payload, err := h.parseUpload(r)
	if err != nil {
		writeUploadError(w, err)
		return
	}

	docID, insertErr := h.store.InsertDocument(r.Context(), user.TenantID,
		payload.name, payload.mimeType, payload.sizeBytes)
	if insertErr != nil {
		log.Printf("rag: insert document: %v", insertErr)
		apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "document registration failed")
		return
	}

	h.audit(r.Context(), "RAG_DOCUMENT_UPLOAD", "rag_document", docID.String(), "INFO",
		user.TenantID, user.ID, r.UserAgent(),
		map[string]any{"name": payload.name, "mime_type": payload.mimeType})

	if h.ingest != nil {
		content := payload.content // capture before request goes away
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("rag: ingest panic recovered doc=%s: %v", docID, rec)
				}
			}()
			h.ingest(h.serverCtx, user.TenantID, docID, content)
		}()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(DocumentResponse{
		ID:        docID.String(),
		Name:      payload.name,
		MimeType:  payload.mimeType,
		SizeBytes: payload.sizeBytes,
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
	})
}

// parseUpload normalizes all accepted upload shapes into one payload:
//
//  1. JSON with "content" (raw text): unchanged legacy path, no conversion.
//  2. JSON with "content_base64": base64-encoded binary; bytes go through
//     the markitdown sidecar and only the markdown reaches ingest.
//  3. Raw request body with any non-JSON Content-Type: binary upload; name
//     comes from the "name" query param or X-File-Name header.
//
// Every rejection here happens BEFORE InsertDocument, so a failed upload
// leaves no document row behind.
func (h *Handler) parseUpload(r *http.Request) (*uploadPayload, *uploadError) {
	mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])
	if mediaType != "" && mediaType != "application/json" {
		return h.parseBinaryUpload(r)
	}

	var req UploadRequest
	// Reader limit accommodates base64 of the full binary cap (4/3 inflation
	// plus padding slack); the per-shape ceilings below enforce the real caps.
	readerLimit := h.maxUploadBytes/3*4 + 65536
	if err := json.NewDecoder(io.LimitReader(r.Body, readerLimit)).Decode(&req); err != nil {
		return nil, &uploadError{status: http.StatusBadRequest, msg: "invalid request body"}
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, &uploadError{status: http.StatusBadRequest, msg: "name required"}
	}
	if req.ContentBase64 == "" {
		if strings.TrimSpace(req.Content) == "" {
			return nil, &uploadError{status: http.StatusBadRequest, msg: "content required"}
		}
		return h.textPayload(req)
	}
	if strings.TrimSpace(req.Content) != "" {
		return nil, &uploadError{status: http.StatusBadRequest, msg: "provide either content or content_base64, not both"}
	}
	return h.base64Payload(r, req)
}

// textPayload is the untouched raw-text passthrough, still capped at the
// historical 10MB JSON ceiling.
func (h *Handler) textPayload(req UploadRequest) (*uploadPayload, *uploadError) {
	if int64(len(req.Content)) > maxRawTextBytes {
		return nil, &uploadError{status: http.StatusRequestEntityTooLarge,
			code: apierrors.CodeRequestTooLarge,
			msg:  fmt.Sprintf("text content exceeds %d byte limit", maxRawTextBytes)}
	}
	if req.MimeType == "" {
		req.MimeType = "text/plain"
	}
	return &uploadPayload{
		name:      req.Name,
		mimeType:  req.MimeType,
		content:   req.Content,
		sizeBytes: int64(len(req.Content)),
	}, nil
}

// base64Payload decodes content_base64 and converts the bytes to markdown.
// Order matters: name presence, then size cap, then format allowlist.
func (h *Handler) base64Payload(r *http.Request, req UploadRequest) (*uploadPayload, *uploadError) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, &uploadError{status: http.StatusBadRequest, msg: "name required"}
	}
	data, err := decodeUploadBase64(req.ContentBase64, h.maxUploadBytes)
	if err != nil {
		return nil, err
	}
	if err := validateBinaryMeta(req.Name, req.MimeType); err != nil {
		return nil, err
	}
	mimeType := req.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	content, uerr := h.convertForIngest(r.Context(), data, req.Name, mimeType)
	if uerr != nil {
		return nil, uerr
	}
	return &uploadPayload{
		name:      req.Name,
		mimeType:  mimeType,
		content:   content,
		sizeBytes: int64(len(data)),
	}, nil
}

// parseBinaryUpload reads a non-JSON request body as raw file bytes.
func (h *Handler) parseBinaryUpload(r *http.Request) (*uploadPayload, *uploadError) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = strings.TrimSpace(r.Header.Get("X-File-Name"))
	}
	if name == "" {
		return nil, &uploadError{status: http.StatusBadRequest, msg: "name required (query param or X-File-Name header)"}
	}
	contentType := mediaTypeOf(r.Header.Get("Content-Type"))
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = ""
	}
	mimeType := contentType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	data, over, err := readBodyCapped(r.Body, h.maxUploadBytes)
	if err != nil {
		log.Printf("rag: binary body read: %v", err)
		return nil, &uploadError{status: http.StatusBadRequest, msg: "failed reading upload body"}
	}
	if over {
		return nil, &uploadError{status: http.StatusRequestEntityTooLarge,
			code: apierrors.CodeRequestTooLarge,
			msg:  fmt.Sprintf("upload exceeds %d byte limit", h.maxUploadBytes)}
	}
	if len(data) == 0 {
		return nil, &uploadError{status: http.StatusBadRequest, msg: "empty request body"}
	}
	if err := validateBinaryMeta(name, mimeType); err != nil {
		return nil, err
	}

	content, uerr := h.convertForIngest(r.Context(), data, name, mimeType)
	if uerr != nil {
		return nil, uerr
	}
	return &uploadPayload{
		name:      name,
		mimeType:  mimeType,
		content:   content,
		sizeBytes: int64(len(data)),
	}, nil
}

// convertForIngest calls the wired Converter and enforces the loud-failure
// contract on this side as well. A nil converter is a loud 503: the sidecar
// is part of the deployment contract for binary uploads.
func (h *Handler) convertForIngest(ctx context.Context, data []byte, filename, mimeType string) (string, *uploadError) {
	if h.converter == nil {
		log.Printf("rag: binary upload %q rejected: converter not wired", filename)
		return "", &uploadError{
			status: http.StatusServiceUnavailable,
			msg:    "binary uploads require the document conversion service",
		}
	}
	if !ExtensionAllowed(filename) && !allowedContentTypes[mimeType] {
		return "", &uploadError{
			status: http.StatusUnprocessableEntity,
			code:   apierrors.CodeInvalidRequest,
			msg:    "unsupported document format",
			class:  "unsupported_format",
		}
	}
	markdown, err := h.converter.Convert(ctx, filename, mimeType, data)
	if err != nil {
		var convErr *ConversionError
		if errors.As(err, &convErr) && convErr.Rejected {
			log.Printf("rag: conversion rejected file=%s class=%s", filename, convErr.Class)
			status := http.StatusUnprocessableEntity
			code := apierrors.CodeInvalidRequest
			if convErr.Class == "payload_too_large" {
				status = http.StatusRequestEntityTooLarge
				code = apierrors.CodeRequestTooLarge
			}
			return "", &uploadError{
				status: status,
				code:   code,
				msg: fmt.Sprintf("document conversion failed (%s): %s",
					truncate(convErr.Class, 64), truncate(convErr.Detail, 300)),
				class: convErr.Class,
			}
		}
		log.Printf("rag: conversion unavailable file=%s: %v", filename, err)
		return "", &uploadError{
			status: http.StatusServiceUnavailable,
			msg:    "document conversion service unavailable",
		}
	}
	if strings.TrimSpace(markdown) == "" {
		return "", &uploadError{
			status: http.StatusUnprocessableEntity,
			code:   apierrors.CodeInvalidRequest,
			msg:    "document conversion produced no text",
			class:  "empty_result",
		}
	}
	return markdown, nil
}

// validateBinaryMeta enforces name + format rules shared by both binary
// shapes. Format acceptance requires BOTH signals to agree when both are
// present: a filename whose extension is allowed AND (when a specific mime
// type is supplied) a mime consistent with that extension. An OR would admit
// mismatched pairs like name=notes.txt with Content-Type application/pdf.
func validateBinaryMeta(name, mimeType string) *uploadError {
	if strings.TrimSpace(name) == "" {
		return &uploadError{status: http.StatusBadRequest, msg: "name required"}
	}
	for _, rr := range name {
		if rr < 0x20 || rr == 0x7f {
			return &uploadError{status: http.StatusBadRequest,
				msg: "name contains control characters"}
		}
	}
	ext := strings.ToLower(filepath.Ext(name))
	mimeAllowed := allowedContentTypes[mimeType]
	switch {
	case ext == "" && !mimeAllowed:
		return &uploadError{status: http.StatusUnprocessableEntity,
			msg: "unsupported document format", class: "unsupported_format"}
	case ext != "":
		if !ExtensionAllowed(name) {
			return &uploadError{status: http.StatusUnprocessableEntity,
				msg: "unsupported document format", class: "unsupported_format"}
		}
		if mimeAllowed && !mimeConsistent(ext, mimeType) {
			return &uploadError{status: http.StatusBadRequest,
				msg: "mime_type does not match the filename extension"}
		}
	}
	return nil
}

// extCanonicalType maps each allowed extension to its canonical mime type so
// consistency checks never depend on the host's mime database (Go's
// mime.TypeByExtension falls back to an OS table that is missing on Alpine).
var extCanonicalType = map[string]string{
	".pdf":  "application/pdf",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".doc":  "application/msword",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".ppt":  "application/vnd.ms-powerpoint",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".xls":  "application/vnd.ms-excel",
	".csv":  "text/csv",
	".json": "application/json",
	".rtf":  "application/rtf",
	".epub": "application/epub+zip",
	".html": "text/html",
	".htm":  "text/html",
	// Multi-alias formats: any of these count as consistent.
	".xml": "application/xml,text/xml",
	".md":  "text/markdown,text/plain",
	".txt": "text/plain",
}

// mimeConsistent reports whether the supplied mime type plausibly matches the
// filename extension. Unknown extension mappings pass (the allowlist already
// gated them); known mismatches fail closed.
func mimeConsistent(ext, mimeType string) bool {
	candidates, ok := extCanonicalType[ext]
	if !ok {
		return true
	}
	base := strings.TrimSpace(strings.Split(strings.ToLower(mimeType), ";")[0])
	for _, candidate := range strings.Split(candidates, ",") {
		if base == strings.TrimSpace(candidate) {
			return true
		}
	}
	return false
}

// decodeUploadBase64 decodes standard base64, tolerating embedded whitespace.
func decodeUploadBase64(encoded string, maxBytes int64) ([]byte, *uploadError) {
	cleaned := strings.Map(func(rr rune) rune {
		if rr == ' ' || rr == '\n' || rr == '\r' || rr == '\t' {
			return -1
		}
		return rr
	}, encoded)
	// 4 base64 chars -> 3 bytes, plus padding slack.
	if int64(len(cleaned))/4*3+3 > maxBytes {
		return nil, &uploadError{
			status: http.StatusRequestEntityTooLarge,
			code:   apierrors.CodeRequestTooLarge,
			msg:    fmt.Sprintf("decoded upload exceeds %d byte limit", maxBytes),
		}
	}
	data, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return nil, &uploadError{status: http.StatusBadRequest, msg: "invalid base64 content"}
	}
	return data, nil
}

// readBodyCapped reads at most max+1 bytes; over reports whether the body
// exceeded the cap (so callers reject without buffering an unbounded body).
func readBodyCapped(body io.Reader, max int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(body, max+1))
	if err != nil {
		return nil, false, err
	}
	return data, int64(len(data)) > max, nil
}

// uploadError is parseUpload's typed failure; writeUploadError renders it.
type uploadError struct {
	status int
	code   apierrors.Code // zero value falls back to CodeInvalidRequest
	msg    string
	class  string // sidecar error class surfaced in the message when set
}

func writeUploadError(w http.ResponseWriter, ue *uploadError) {
	apierrors.Write(w, ue.status, orDefaultCode(ue.code), ue.msg)
}

func orDefaultCode(c apierrors.Code) apierrors.Code {
	if c == "" {
		return apierrors.CodeInvalidRequest
	}
	return c
}

func mediaTypeOf(header string) string {
	return strings.TrimSpace(strings.Split(header, ";")[0])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}

	docs, err := h.store.ListDocuments(r.Context(), user.TenantID)
	if err != nil {
		log.Printf("rag: list documents: %v", err)
		apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "list failed")
		return
	}

	results := make([]DocumentResponse, len(docs))
	for i, d := range docs {
		results[i] = docRowToResponse(d)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": results, "object": "list"})
}

func (h *Handler) handleGetDocument(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}

	docID, err := extractDocID(r.URL.Path)
	if err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid document id")
		return
	}

	doc, err := h.store.GetDocument(r.Context(), user.TenantID, docID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			apierrors.Write(w, http.StatusNotFound, apierrors.CodeInvalidRequest, "document not found")
		} else {
			log.Printf("rag: get document: %v", err)
			apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "request failed")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(docRowToResponse(doc))
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}

	docID, err := extractDocID(r.URL.Path)
	if err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid document id")
		return
	}

	found, err := h.store.DeleteDocument(r.Context(), user.TenantID, docID)
	if err != nil {
		log.Printf("rag: delete document: %v", err)
		apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "delete failed")
		return
	}
	if !found {
		apierrors.Write(w, http.StatusNotFound, apierrors.CodeInvalidRequest, "document not found")
		return
	}

	// Audit only fires when a row was actually removed (regulatory requirement).
	h.audit(r.Context(), "RAG_DOCUMENT_DELETE", "rag_document", docID.String(), "INFO",
		user.TenantID, user.ID, r.UserAgent(), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apierrors.Write(w, http.StatusMethodNotAllowed, apierrors.CodeInvalidRequest, "method not allowed")
		return
	}

	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}

	var req SearchRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "query required")
		return
	}
	if req.TopK <= 0 {
		req.TopK = 5
	}
	// ponytail: cap prevents huge vector scans and audit event floods.
	if req.TopK > maxTopK {
		req.TopK = maxTopK
	}

	// Project scope (issue #1595): narrow retrieval to one project's documents.
	projectID := uuid.Nil
	if raw := strings.TrimSpace(req.ProjectID); raw != "" {
		parsed, perr := uuid.Parse(raw)
		if perr != nil {
			apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid project_id")
			return
		}
		projectID = parsed
	}

	// Authorization BEFORE any filtering (issue #1595 acceptance criterion 6).
	//
	// Ordered ahead of the embedding guard, the audit event and the embed call
	// on purpose: an unauthorized project id must cost nothing, and it must
	// leave no RAG_SEARCH or RAG_CHUNK_RETRIEVED trail naming documents the
	// caller may not read. Filtering by a client supplied project id without
	// this check would hand one member of a tenant another member's passages,
	// because row level security keys on the tenant and both members present
	// the same tenant.
	if projectID != uuid.Nil {
		if aerr := h.authorizeProject(r.Context(), user.TenantID, user.ID, projectID); aerr != nil {
			writeProjectError(w, aerr)
			return
		}
	}

	if !h.checkEmbeddingGuard(r.Context(), user.TenantID) {
		apierrors.Write(w, http.StatusServiceUnavailable, apierrors.CodeServiceUnavailable, "embedding model changed, re-embed required")
		return
	}

	h.audit(r.Context(), "RAG_SEARCH", "rag_document", user.TenantID.String(), "INFO",
		user.TenantID, user.ID, r.UserAgent(), map[string]any{"top_k": req.TopK})

	vec, err := h.embed.Embed(r.Context(), req.Query)
	if err != nil {
		apierrors.Write(w, http.StatusServiceUnavailable, apierrors.CodeServiceUnavailable, "search service unavailable")
		return
	}

	chunks, err := h.store.SearchChunks(r.Context(), user.TenantID, vec, req.TopK, projectID)
	if err != nil {
		log.Printf("rag: search chunks: %v", err)
		apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "search failed")
		return
	}

	results := make([]ChunkResult, len(chunks))
	for i, c := range chunks {
		results[i] = ChunkResult{
			ChunkID:      c.ID.String(),
			DocumentID:   c.DocumentID.String(),
			DocumentName: c.DocumentName,
			Content:      c.Content,
			Score:        c.Score,
		}
		// RAG_CHUNK_RETRIEVED: one event per chunk (Law 25 / PHIPA requirement).
		h.audit(r.Context(), "RAG_CHUNK_RETRIEVED", "rag_chunk", c.ID.String(), "INFO",
			user.TenantID, user.ID, r.UserAgent(), map[string]any{
				"score":       c.Score,
				"document_id": c.DocumentID.String(),
			})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(SearchResponse{Results: results})
}

func extractDocID(path string) (uuid.UUID, error) {
	trimmed := strings.TrimSuffix(path, "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx < 0 {
		return uuid.Nil, errors.New("missing id segment")
	}
	return uuid.Parse(trimmed[idx+1:])
}

func docRowToResponse(d DocRow) DocumentResponse {
	return DocumentResponse{
		ID:        d.ID.String(),
		Name:      d.Name,
		MimeType:  d.MimeType,
		SizeBytes: d.SizeBytes,
		Status:    d.Status,
		CreatedAt: d.CreatedAt,
	}
}
