package artifactsclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCreate_SendsBearerJWTAndDecodesResponse(t *testing.T) {
	var gotAuth, gotPath, gotMethod string
	var gotBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":            "artifact-1",
			"version":       1,
			"url":           "https://artifacts.example/artifacts/artifact-1",
			"versioned_url": "https://artifacts.example/artifacts/artifact-1/v/1",
			"created_at":    time.Now().UTC().Format(time.RFC3339),
		})
	}))
	defer srv.Close()

	c := New(srv.URL)
	got, err := c.Create(context.Background(), "test-jwt", "Q3 deck", "<html></html>")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/artifacts" {
		t.Fatalf("path = %q, want /v1/artifacts", gotPath)
	}
	if gotAuth != "Bearer test-jwt" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer test-jwt")
	}
	if gotBody["name"] != "Q3 deck" || gotBody["html"] != "<html></html>" {
		t.Fatalf("request body = %+v, want name/html echoed", gotBody)
	}
	if got.ID != "artifact-1" || got.Version != 1 || got.URL == "" || got.VersionedURL == "" {
		t.Fatalf("Create() = %+v, want decoded artifact", got)
	}
}

func TestCreate_RequiresBearerJWT(t *testing.T) {
	c := New("http://unused.invalid")
	if _, err := c.Create(context.Background(), "", "name", "<html></html>"); err == nil {
		t.Fatal("expected error for empty bearer JWT, got nil")
	}
}

func TestCreate_PropagatesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.Create(context.Background(), "test-jwt", "name", "<html></html>"); err == nil {
		t.Fatal("expected error for a 401 response, got nil")
	}
}

func TestAddVersion_PostsToVersionsPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "artifact-1", "version": 2,
			"url":           "https://artifacts.example/artifacts/artifact-1",
			"versioned_url": "https://artifacts.example/artifacts/artifact-1/v/2",
			"created_at":    time.Now().UTC().Format(time.RFC3339),
		})
	}))
	defer srv.Close()

	c := New(srv.URL)
	got, err := c.AddVersion(context.Background(), "test-jwt", "artifact-1", "<html>v2</html>")
	if err != nil {
		t.Fatalf("AddVersion returned error: %v", err)
	}
	if gotPath != "/v1/artifacts/artifact-1/versions" {
		t.Fatalf("path = %q, want /v1/artifacts/artifact-1/versions", gotPath)
	}
	if got.Version != 2 {
		t.Fatalf("Version = %d, want 2", got.Version)
	}
}

func TestCreate_RejectsEmptyHTML(t *testing.T) {
	c := New("http://unused.invalid")
	if _, err := c.Create(context.Background(), "test-jwt", "name", ""); err == nil {
		t.Fatal("expected error for empty html, got nil")
	}
}
