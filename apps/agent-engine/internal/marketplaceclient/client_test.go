package marketplaceclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/marketplaceclient"
)

func TestEnabled_SendsExpectedRequestAndDecodesResponse(t *testing.T) {
	tenantID := uuid.New()
	const token = "test-internal-token"

	var gotPath, gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeader = r.Header.Get(marketplaceclient.InternalTokenHeader)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenant_id": tenantID,
			"servers": []map[string]any{
				{"name": "github", "config": map[string]any{"command": "npx", "args": []string{"-y", "server-github"}}},
			},
		})
	}))
	defer srv.Close()

	c := marketplaceclient.New(srv.URL, token)
	entries, err := c.Enabled(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("Enabled: %v", err)
	}

	wantPath := "/internal/marketplace/" + tenantID.String() + "/mcp-servers"
	if gotPath != wantPath {
		t.Fatalf("expected path %q, got %q", wantPath, gotPath)
	}
	if gotHeader != token {
		t.Fatalf("expected %s header %q, got %q", marketplaceclient.InternalTokenHeader, token, gotHeader)
	}
	if len(entries) != 1 || entries[0].Name != "github" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

func TestEnabled_EmptyServersIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"tenant_id": uuid.New(), "servers": []any{}})
	}))
	defer srv.Close()

	c := marketplaceclient.New(srv.URL, "tok")
	entries, err := c.Enabled(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("Enabled: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestEnabled_NonOKStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := marketplaceclient.New(srv.URL, "tok")
	if _, err := c.Enabled(context.Background(), uuid.New()); err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestEnabled_MalformedJSONIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := marketplaceclient.New(srv.URL, "tok")
	if _, err := c.Enabled(context.Background(), uuid.New()); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestEnabled_UnreachableServerIsError(t *testing.T) {
	c := marketplaceclient.New("http://127.0.0.1:1", "tok") // port 1: nothing listens, connection refused
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.Enabled(ctx, uuid.New()); err == nil {
		t.Fatal("expected error for unreachable control-plane")
	}
}
