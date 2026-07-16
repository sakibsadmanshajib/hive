package rag

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// maxIngestBodyBytes bounds the ingest request body the same way edge-api's
// own /v1/rag/documents upload handler bounds its body (10 MiB), so a caller
// cannot force unbounded memory use and embedding work on control-plane.
const maxIngestBodyBytes = 10 * 1024 * 1024

// IngestFunc chunks, embeds, and stores a document. It matches the method
// value of (*Ingester).Ingest, kept as a func type so tests can inject a fake
// without a real database or embedding backend.
type IngestFunc func(ctx context.Context, tenantID, docID uuid.UUID, content string) error

// IngestHandler serves POST /internal/rag/ingest, the service-to-service
// endpoint edge-api calls after registering a rag_documents row so the
// document gets chunked, embedded, and stored (blueprint Step 2.1, #232).
type IngestHandler struct {
	ingest IngestFunc
}

// NewIngestHandler constructs an IngestHandler around ingest.
func NewIngestHandler(ingest IngestFunc) *IngestHandler {
	return &IngestHandler{ingest: ingest}
}

type ingestRequest struct {
	TenantID   string `json:"tenant_id"`
	DocumentID string `json:"document_id"`
	Content    string `json:"content"`
}

func (h *IngestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxIngestBodyBytes)
	var req ingestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	if strings.TrimSpace(req.Content) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content is required"})
		return
	}
	tenantID, err := uuid.Parse(strings.TrimSpace(req.TenantID))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id must be a valid uuid"})
		return
	}
	docID, err := uuid.Parse(strings.TrimSpace(req.DocumentID))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "document_id must be a valid uuid"})
		return
	}

	if err := h.ingest(r.Context(), tenantID, docID, req.Content); err != nil {
		// Provider-blind: log the real cause server-side, never echo it back --
		// Ingester wraps some errors (e.g. DB failures) that are not yet
		// sanitized text, so this boundary must not trust err.Error() as safe.
		log.Printf("rag: ingest failed tenant=%s doc=%s: %v", tenantID, docID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "ingest failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// RegisterRoutes mounts the /internal/rag/* routes on mux, wrapped by gate
// (typically platformhttp.RequireInternalToken). A nil gate registers the
// handler directly, matching filestore.RegisterRoutes.
func RegisterRoutes(mux *http.ServeMux, ingest IngestFunc, gate func(http.Handler) http.Handler) {
	h := NewIngestHandler(ingest)
	if gate == nil {
		gate = func(next http.Handler) http.Handler { return next }
	}
	mux.Handle("/internal/rag/ingest", gate(h))
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
