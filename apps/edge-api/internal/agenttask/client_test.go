package agenttask

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/cpauth"
)

func TestClient_Create_PostsExpectedPathAndBody(t *testing.T) {
	cpauth.SetTokenForTest("test-internal-token")
	defer cpauth.SetTokenForTest("")

	tenantID, userID := uuid.New(), uuid.New()
	var gotPath, gotMethod, gotToken string
	var gotBody struct {
		Pack string `json:"pack"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotToken = r.Header.Get(cpauth.Header)
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Task{ID: uuid.New().String(), Pack: "coding-pack", Status: "queued"})
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	task, err := client.Create(context.Background(), tenantID, userID, "coding-pack", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	wantPath := "/internal/agent-tasks/" + tenantID.String() + "/" + userID.String()
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
	if gotToken != "test-internal-token" {
		t.Errorf("internal auth header = %q, want %q", gotToken, "test-internal-token")
	}
	if gotBody.Pack != "coding-pack" {
		t.Errorf("request body pack = %q, want coding-pack", gotBody.Pack)
	}
	if task.Status != "queued" {
		t.Errorf("response status = %q, want queued", task.Status)
	}
}

func TestClient_Create_BadRequestMapsToErrInvalidPack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"pack must be coding-pack or knowledge-work-pack"}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	_, err := client.Create(context.Background(), uuid.New(), uuid.New(), "not-a-pack", "")
	if err != ErrInvalidPack {
		t.Fatalf("expected ErrInvalidPack, got %v", err)
	}
}

func TestClient_Get_NotFoundMapsToErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	_, err := client.Get(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestClient_Cancel_ConflictMapsToErrTerminalState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	_, err := client.Cancel(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if err != ErrTerminalState {
		t.Fatalf("expected ErrTerminalState, got %v", err)
	}
}

func TestClient_List_ReturnsTasksOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string][]Task{
			"tasks": {{ID: uuid.New().String(), Pack: "coding-pack", Status: "queued"}},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	tasks, err := client.List(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
}

func TestClient_Get_UnreachableServerReturnsError(t *testing.T) {
	client := NewClient("http://127.0.0.1:1")
	_, err := client.Get(context.Background(), uuid.New(), uuid.New(), uuid.New())
	if err == nil {
		t.Fatal("expected error when control-plane is unreachable")
	}
}
