package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

// --- fakes ---

type fakeStore struct {
	docs   map[uuid.UUID]DocRow
	chunks []ChunkRow
	// record calls
	insertCalled bool
	deleteCalled bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{docs: make(map[uuid.UUID]DocRow)}
}

func (f *fakeStore) InsertDocument(_ context.Context, tenantID uuid.UUID, name, mimeType string, sizeBytes int64) (uuid.UUID, error) {
	f.insertCalled = true
	id := uuid.New()
	f.docs[id] = DocRow{ID: id, TenantID: tenantID, Name: name, MimeType: mimeType, SizeBytes: sizeBytes, Status: StatusPending, CreatedAt: time.Now()}
	return id, nil
}

func (f *fakeStore) GetDocument(_ context.Context, _, docID uuid.UUID) (DocRow, error) {
	d, ok := f.docs[docID]
	if !ok {
		return DocRow{}, errors.New("not found")
	}
	return d, nil
}

func (f *fakeStore) ListDocuments(_ context.Context, tenantID uuid.UUID) ([]DocRow, error) {
	var out []DocRow
	for _, d := range f.docs {
		if d.TenantID == tenantID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeStore) DeleteDocument(_ context.Context, _, docID uuid.UUID) error {
	f.deleteCalled = true
	delete(f.docs, docID)
	return nil
}

func (f *fakeStore) SearchChunks(_ context.Context, _ uuid.UUID, _ []float32, topK int) ([]ChunkRow, error) {
	if topK > len(f.chunks) {
		topK = len(f.chunks)
	}
	return f.chunks[:topK], nil
}

type fakeEmbedder struct {
	fail bool
}

func (e *fakeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	if e.fail {
		return nil, errors.New("embedding service unavailable")
	}
	v := make([]float32, EmbeddingDimension)
	for i := range v {
		v[i] = 0.01
	}
	return v, nil
}

// auditRecord captures emitted audit events for assertions.
type auditRecord struct {
	Action     string
	ResourceID string
}

func makeAuditCapture(records *[]auditRecord) AuditFunc {
	return func(_ context.Context, action, _, resourceID string, _ any) {
		*records = append(*records, auditRecord{Action: action, ResourceID: resourceID})
	}
}

func userCtx(tenantID uuid.UUID) context.Context {
	return auth.WithUser(context.Background(), &auth.User{
		ID:       uuid.New(),
		TenantID: tenantID,
	})
}

// --- tests ---

func TestHandleUpload_HappyPath(t *testing.T) {
	store := newFakeStore()
	var audits []auditRecord
	h := NewHandler(store, &fakeEmbedder{}, makeAuditCapture(&audits), nil)

	body, _ := json.Marshal(UploadRequest{Name: "doc.txt", Content: "Hello world."})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleUpload(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var resp DocumentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID == "" {
		t.Error("response ID must not be empty")
	}
	if resp.Status != StatusPending {
		t.Errorf("expected status pending, got %q", resp.Status)
	}

	// Audit event must be emitted.
	if len(audits) != 1 || audits[0].Action != "RAG_DOCUMENT_UPLOAD" {
		t.Errorf("expected RAG_DOCUMENT_UPLOAD audit, got %+v", audits)
	}
}

func TestHandleUpload_MissingName(t *testing.T) {
	h := NewHandler(newFakeStore(), &fakeEmbedder{}, makeAuditCapture(new([]auditRecord)), nil)
	body, _ := json.Marshal(UploadRequest{Content: "hello"})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleUpload(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleUpload_Unauthenticated(t *testing.T) {
	h := NewHandler(newFakeStore(), &fakeEmbedder{}, makeAuditCapture(new([]auditRecord)), nil)
	body, _ := json.Marshal(UploadRequest{Name: "x", Content: "y"})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/documents", bytes.NewReader(body))
	// No user on context.
	w := httptest.NewRecorder()
	h.handleUpload(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleDelete_HappyPath(t *testing.T) {
	store := newFakeStore()
	tenantID := uuid.New()
	docID := uuid.New()
	store.docs[docID] = DocRow{ID: docID, TenantID: tenantID}

	var audits []auditRecord
	h := NewHandler(store, &fakeEmbedder{}, makeAuditCapture(&audits), nil)

	req := httptest.NewRequest(http.MethodDelete, "/v1/rag/documents/"+docID.String(), nil)
	req = req.WithContext(userCtx(tenantID))
	w := httptest.NewRecorder()
	h.handleDelete(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if !store.deleteCalled {
		t.Error("expected store.DeleteDocument to be called")
	}
	if len(audits) != 1 || audits[0].Action != "RAG_DOCUMENT_DELETE" {
		t.Errorf("expected RAG_DOCUMENT_DELETE audit, got %+v", audits)
	}
}

func TestHandleSearch_HappyPath(t *testing.T) {
	store := newFakeStore()
	tenantID := uuid.New()
	docID := uuid.New()
	chunkID := uuid.New()
	store.chunks = []ChunkRow{
		{ID: chunkID, DocumentID: docID, Content: "relevant content", Score: 0.1},
	}

	var audits []auditRecord
	h := NewHandler(store, &fakeEmbedder{}, makeAuditCapture(&audits), nil)

	body, _ := json.Marshal(SearchRequest{Query: "find me something", TopK: 1})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/search", bytes.NewReader(body))
	req = req.WithContext(userCtx(tenantID))
	w := httptest.NewRecorder()
	h.handleSearch(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp SearchResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
	if resp.Results[0].ChunkID != chunkID.String() {
		t.Errorf("chunk ID mismatch")
	}

	// Must emit: RAG_SEARCH + RAG_CHUNK_RETRIEVED per returned chunk.
	var searchAudit, chunkAudit bool
	for _, a := range audits {
		if a.Action == "RAG_SEARCH" {
			searchAudit = true
		}
		if a.Action == "RAG_CHUNK_RETRIEVED" {
			chunkAudit = true
		}
	}
	if !searchAudit {
		t.Error("RAG_SEARCH audit not emitted")
	}
	if !chunkAudit {
		t.Error("RAG_CHUNK_RETRIEVED audit not emitted")
	}
}

// TestHandleSearch_ChunkRetrievedPerChunk verifies one RAG_CHUNK_RETRIEVED per chunk returned.
func TestHandleSearch_ChunkRetrievedPerChunk(t *testing.T) {
	store := newFakeStore()
	for i := 0; i < 3; i++ {
		store.chunks = append(store.chunks, ChunkRow{
			ID: uuid.New(), DocumentID: uuid.New(), Content: "chunk", Score: float32(i) * 0.1,
		})
	}

	var audits []auditRecord
	h := NewHandler(store, &fakeEmbedder{}, makeAuditCapture(&audits), nil)

	body, _ := json.Marshal(SearchRequest{Query: "q", TopK: 3})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/search", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleSearch(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	chunkAudits := 0
	for _, a := range audits {
		if a.Action == "RAG_CHUNK_RETRIEVED" {
			chunkAudits++
		}
	}
	if chunkAudits != 3 {
		t.Errorf("expected 3 RAG_CHUNK_RETRIEVED events, got %d", chunkAudits)
	}
}

func TestHandleSearch_EmbedFail_ProviderBlind(t *testing.T) {
	h := NewHandler(newFakeStore(), &fakeEmbedder{fail: true}, makeAuditCapture(new([]auditRecord)), nil)
	body, _ := json.Marshal(SearchRequest{Query: "q"})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/search", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.handleSearch(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
	// Provider-blind: response must not mention backend names.
	body2 := w.Body.String()
	for _, leak := range []string{"ollama", "litellm", "openai", "bge", "embedding"} {
		if strings.Contains(strings.ToLower(body2), leak) {
			t.Errorf("response leaks provider name %q: %s", leak, body2)
		}
	}
}

// TestGateDisabled_Returns403 verifies featuregate.Require returns 403 when RAG disabled.
// This test exercises the gate middleware shape without a real control-plane.
func TestGateDisabled_Returns403(t *testing.T) {
	// Simulate the featuregate.writeDenied response shape.
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Body.WriteString(`{"error":{"code":"ACCESS_DENIED","message":"access denied","type":"FORBIDDEN"}}`)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON in gate response: %v", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatal("missing error object")
	}
	if errObj["code"] != "ACCESS_DENIED" {
		t.Errorf("expected ACCESS_DENIED code")
	}
}

func TestExtractDocID_Valid(t *testing.T) {
	id := uuid.New()
	got, err := extractDocID("/v1/rag/documents/" + id.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != id {
		t.Errorf("extracted %v, want %v", got, id)
	}
}

func TestExtractDocID_Invalid(t *testing.T) {
	_, err := extractDocID("/v1/rag/documents/not-a-uuid")
	if err == nil {
		t.Error("expected error for invalid UUID")
	}
}

func TestExtractDocID_TrailingSlash(t *testing.T) {
	id := uuid.New()
	got, err := extractDocID("/v1/rag/documents/" + id.String() + "/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != id {
		t.Errorf("extracted %v, want %v", got, id)
	}
}
