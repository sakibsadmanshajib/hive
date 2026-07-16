package rag

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

const maxTopK = 100

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
	SearchChunks(ctx context.Context, tenantID uuid.UUID, queryVec []float32, topK int) ([]ChunkRow, error)
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
}

// NewHandler constructs a Handler.
// serverCtx must be the process-level context so ingest goroutines respect shutdown.
func NewHandler(s store, embed Embedder, audit AuditFunc, ingest IngestFunc, serverCtx context.Context) *Handler {
	if serverCtx == nil {
		serverCtx = context.Background()
	}
	return &Handler{store: s, embed: embed, audit: audit, ingest: ingest, serverCtx: serverCtx}
}

// Register mounts all /v1/rag/* routes on mux.
// Callers wrap with featuregate.Require(FeatureRAG) before mounting.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/rag/documents", h.routeDocuments)
	mux.HandleFunc("/v1/rag/documents/", h.routeDocumentByID)
	mux.HandleFunc("/v1/rag/search", h.handleSearch)
	mux.HandleFunc("/v1/rag/chat", h.handleChat)
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

	h.audit(r.Context(), "RAG_DOCUMENT_UPLOAD", "rag_document", docID.String(), "INFO",
		user.TenantID, user.ID, r.UserAgent(), map[string]any{"name": req.Name, "mime_type": req.MimeType})

	if h.ingest != nil {
		content := req.Content // capture before request goes away
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
		Name:      req.Name,
		MimeType:  req.MimeType,
		SizeBytes: int64(len(req.Content)),
		Status:    StatusPending,
		CreatedAt: time.Now().UTC(),
	})
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

	h.audit(r.Context(), "RAG_SEARCH", "rag_document", user.TenantID.String(), "INFO",
		user.TenantID, user.ID, r.UserAgent(), map[string]any{"top_k": req.TopK})

	vec, err := h.embed.Embed(r.Context(), req.Query)
	if err != nil {
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
