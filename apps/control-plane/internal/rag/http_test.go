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

	"github.com/google/uuid"
)

func TestHandleIngest(t *testing.T) {
	validTenant := uuid.New()
	validDoc := uuid.New()

	tests := []struct {
		name       string
		body       string
		ingestErr  error
		wantStatus int
		wantCalled bool
	}{
		{
			name:       "valid request calls ingest and returns ok",
			body:       `{"tenant_id":"` + validTenant.String() + `","document_id":"` + validDoc.String() + `","content":"hello world"}`,
			wantStatus: http.StatusOK,
			wantCalled: true,
		},
		{
			name:       "missing tenant_id is rejected",
			body:       `{"document_id":"` + validDoc.String() + `","content":"hello"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing document_id is rejected",
			body:       `{"tenant_id":"` + validTenant.String() + `","content":"hello"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty content is rejected",
			body:       `{"tenant_id":"` + validTenant.String() + `","document_id":"` + validDoc.String() + `","content":""}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid tenant_id uuid is rejected",
			body:       `{"tenant_id":"not-a-uuid","document_id":"` + validDoc.String() + `","content":"hello"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "malformed json is rejected",
			body:       `{not json`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "ingest failure returns 500",
			body:       `{"tenant_id":"` + validTenant.String() + `","document_id":"` + validDoc.String() + `","content":"hello"}`,
			ingestErr:  errors.New("embedding service unavailable"),
			wantStatus: http.StatusInternalServerError,
			wantCalled: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var called bool
			var gotTenant, gotDoc uuid.UUID
			var gotContent string
			fakeIngest := func(_ context.Context, tenantID, docID uuid.UUID, content string) error {
				called = true
				gotTenant, gotDoc, gotContent = tenantID, docID, content
				return tc.ingestErr
			}

			h := NewIngestHandler(fakeIngest)
			req := httptest.NewRequest(http.MethodPost, "/internal/rag/ingest", bytes.NewBufferString(tc.body))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if called != tc.wantCalled {
				t.Fatalf("ingest called = %v, want %v", called, tc.wantCalled)
			}
			if tc.wantCalled && tc.ingestErr == nil {
				if gotTenant != validTenant {
					t.Errorf("tenant_id = %v, want %v", gotTenant, validTenant)
				}
				if gotDoc != validDoc {
					t.Errorf("document_id = %v, want %v", gotDoc, validDoc)
				}
				if gotContent != "hello world" {
					t.Errorf("content = %q, want %q", gotContent, "hello world")
				}
				var resp map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if resp["status"] != "ok" {
					t.Errorf("response status = %q, want %q", resp["status"], "ok")
				}
			}
		})
	}
}

func TestHandleIngest_RejectsOversizedBody(t *testing.T) {
	var called bool
	h := NewIngestHandler(func(context.Context, uuid.UUID, uuid.UUID, string) error {
		called = true
		return nil
	})

	oversized := strings.Repeat("a", maxIngestBodyBytes+1024)
	body := `{"tenant_id":"` + uuid.New().String() + `","document_id":"` + uuid.New().String() + `","content":"` + oversized + `"}`
	req := httptest.NewRequest(http.MethodPost, "/internal/rag/ingest", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	if called {
		t.Fatal("ingest must not be called for an oversized body")
	}
}

func TestHandleIngest_RejectsWrongMethod(t *testing.T) {
	h := NewIngestHandler(func(context.Context, uuid.UUID, uuid.UUID, string) error { return nil })
	req := httptest.NewRequest(http.MethodGet, "/internal/rag/ingest", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestRegisterRoutes_MountsIngestRoute(t *testing.T) {
	var called bool
	fakeIngest := func(context.Context, uuid.UUID, uuid.UUID, string) error {
		called = true
		return nil
	}

	mux := http.NewServeMux()
	RegisterRoutes(mux, fakeIngest, nil)

	tenantID, docID := uuid.New(), uuid.New()
	body := `{"tenant_id":"` + tenantID.String() + `","document_id":"` + docID.String() + `","content":"hi"}`
	req := httptest.NewRequest(http.MethodPost, "/internal/rag/ingest", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !called {
		t.Fatal("expected ingest to be called through the mounted route")
	}
}

func TestRegisterRoutes_GateRejectsUnauthorized(t *testing.T) {
	fakeIngest := func(context.Context, uuid.UUID, uuid.UUID, string) error { return nil }
	denyGate := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
	}

	mux := http.NewServeMux()
	RegisterRoutes(mux, fakeIngest, denyGate)

	req := httptest.NewRequest(http.MethodPost, "/internal/rag/ingest", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
