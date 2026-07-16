package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/cpauth"
)

func TestIngestClient_Ingest_PostsExpectedBody(t *testing.T) {
	cpauth.SetTokenForTest("test-internal-token")
	defer cpauth.SetTokenForTest("")

	tenantID, docID := uuid.New(), uuid.New()
	var gotPath, gotMethod, gotToken string
	var gotBody ingestRequestBody

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotToken = r.Header.Get(cpauth.Header)
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	client := NewIngestClient(srv.URL)
	if err := client.Ingest(context.Background(), tenantID, docID, "hello world"); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/internal/rag/ingest" {
		t.Errorf("path = %q, want /internal/rag/ingest", gotPath)
	}
	if gotToken != "test-internal-token" {
		t.Errorf("internal auth header = %q, want %q", gotToken, "test-internal-token")
	}
	if gotBody.TenantID != tenantID.String() {
		t.Errorf("tenant_id = %q, want %q", gotBody.TenantID, tenantID.String())
	}
	if gotBody.DocumentID != docID.String() {
		t.Errorf("document_id = %q, want %q", gotBody.DocumentID, docID.String())
	}
	if gotBody.Content != "hello world" {
		t.Errorf("content = %q, want %q", gotBody.Content, "hello world")
	}
}

func TestIngestClient_Ingest_NonOKStatusReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"embedding service unavailable"}`))
	}))
	defer srv.Close()

	client := NewIngestClient(srv.URL)
	err := client.Ingest(context.Background(), uuid.New(), uuid.New(), "hello")
	if err == nil {
		t.Fatal("expected error for non-OK status")
	}
}

func TestIngestClient_Ingest_UnreachableServerReturnsError(t *testing.T) {
	client := NewIngestClient("http://127.0.0.1:1")
	err := client.Ingest(context.Background(), uuid.New(), uuid.New(), "hello")
	if err == nil {
		t.Fatal("expected error when control-plane is unreachable")
	}
}
