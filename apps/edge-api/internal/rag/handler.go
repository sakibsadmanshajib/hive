package rag

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// AuditFunc emits an audit event. The shape avoids importing the audit package
// here to prevent a cyclic dependency; main.go wires the real logger.
type AuditFunc func(ctx context.Context, action, resourceType, resourceID string, after any)

// IngestFunc is called asynchronously after document registration to chunk,
// embed, and store the document. In production main.go wires the control-plane
// ingest worker via an HTTP call or direct function.
type IngestFunc func(ctx context.Context, tenantID, docID uuid.UUID, content string)

// store is the minimal interface the handler needs from Repo.
type store interface {
	InsertDocument(ctx context.Context, tenantID uuid.UUID, name, mimeType string, sizeBytes int64) (uuid.UUID, error)
	GetDocument(ctx context.Context, tenantID, docID uuid.UUID) (DocRow, error)
	ListDocuments(ctx context.Context, tenantID uuid.UUID) ([]DocRow, error)
	DeleteDocument(ctx context.Context, tenantID, docID uuid.UUID) error
	SearchChunks(ctx context.Context, tenantID uuid.UUID, queryVec []float32, topK int) ([]ChunkRow, error)
}

// Handler serves /v1/rag/* routes.
type Handler struct {
	store   store
	embed   Embedder
	audit   AuditFunc
	ingest  IngestFunc
}

// NewHandler constructs a Handler. All fields required.
func NewHandler(s store, embed Embedder, audit AuditFunc, ingest IngestFunc) *Handler {
	return &Handler{store: s, embed: embed, audit: audit, ingest: ingest}
}

// Register mounts all /v1/rag/* routes on mux.
// Callers wrap the returned routes with featuregate.Require(FeatureRAG).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/rag/documents", h.routeDocuments)
	mux.HandleFunc("/v1/rag/documents/", h.routeDocumentByID)
	mux.HandleFunc("/v1/rag/search", h.handleSearch)
}

// routeDocuments dispatches POST (upload) and GET (list).
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

// routeDocumentByID dispatches GET (single doc) and DELETE.
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

// handleUpload handles POST /v1/rag/documents.
func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}

	var req UploadRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 10*1024*1024)).Decode(&req); err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "name required")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "content required")
		return
	}
	if req.MimeType == "" {
		req.MimeType = "text/plain"
	}

	docID, err := h.store.InsertDocument(r.Context(), user.TenantID, req.Name, req.MimeType, int64(len(req.Content)))
	if err != nil {
		log.Printf("rag: insert document: %v", err)
		apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "document registration failed")
		return
	}

	h.audit(r.Context(), "RAG_DOCUMENT_UPLOAD", "rag_document", docID.String(), map[string]any{
		"name":      req.Name,
		"mime_type": req.MimeType,
	})

	// Ingest asynchronously so the upload call returns immediately.
	if h.ingest != nil {
		go h.ingest(context.Background(), user.TenantID, docID, req.Content)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(DocumentResponse{
		ID:        docID.String(),
		Name:      req.Name,
		MimeType:  req.MimeType,
		SizeBytes: int64(len(req.Content)),
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
	})
}

// handleList handles GET /v1/rag/documents.
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

// handleGetDocument handles GET /v1/rag/documents/{id}.
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
		apierrors.Write(w, http.StatusNotFound, apierrors.CodeInvalidRequest, "document not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(docRowToResponse(doc))
}

// handleDelete handles DELETE /v1/rag/documents/{id}.
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

	if err := h.store.DeleteDocument(r.Context(), user.TenantID, docID); err != nil {
		log.Printf("rag: delete document: %v", err)
		apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "delete failed")
		return
	}

	h.audit(r.Context(), "RAG_DOCUMENT_DELETE", "rag_document", docID.String(), nil)

	w.WriteHeader(http.StatusNoContent)
}

// handleSearch handles POST /v1/rag/search.
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

	h.audit(r.Context(), "RAG_SEARCH", "rag_corpus", user.TenantID.String(), map[string]any{
		"top_k": req.TopK,
	})

	vec, err := h.embed.Embed(r.Context(), req.Query)
	if err != nil {
		// Provider-blind: do not expose embedding backend identity.
		apierrors.Write(w, http.StatusServiceUnavailable, apierrors.CodeServiceUnavailable, "search service unavailable")
		return
	}

	chunks, err := h.store.SearchChunks(r.Context(), user.TenantID, vec, req.TopK)
	if err != nil {
		log.Printf("rag: search chunks: %v", err)
		apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "search failed")
		return
	}

	results := make([]ChunkResult, len(chunks))
	for i, c := range chunks {
		results[i] = ChunkResult{
			ChunkID:    c.ID.String(),
			DocumentID: c.DocumentID.String(),
			Content:    c.Content,
			Score:      c.Score,
		}
		// RAG_CHUNK_RETRIEVED: one event per returned chunk (regulatory requirement).
		h.audit(r.Context(), "RAG_CHUNK_RETRIEVED", "rag_chunk", c.ID.String(), map[string]any{
			"score":       c.Score,
			"document_id": c.DocumentID.String(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(SearchResponse{Results: results})
}

// extractDocID parses the trailing UUID from a path like /v1/rag/documents/{id}.
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
