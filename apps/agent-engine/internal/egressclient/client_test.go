package egressclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/egressclient"
)

func TestEffective_SendsExpectedRequestAndDecodesResponse(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	const token = "test-internal-token"

	var gotPath, gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeader = r.Header.Get(egressclient.InternalTokenHeader)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenant_id":     tenantID,
			"user_id":       userID,
			"allowed_hosts": []string{"api.example.com", "10.0.0.5"},
		})
	}))
	defer srv.Close()

	c := egressclient.New(srv.URL, token)
	hosts, err := c.Effective(context.Background(), tenantID, userID)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}

	wantPath := "/internal/egress-policy/" + tenantID.String() + "/" + userID.String()
	if gotPath != wantPath {
		t.Fatalf("expected path %q, got %q", wantPath, gotPath)
	}
	if gotHeader != token {
		t.Fatalf("expected %s header %q, got %q", egressclient.InternalTokenHeader, token, gotHeader)
	}
	if len(hosts) != 2 || hosts[0] != "api.example.com" || hosts[1] != "10.0.0.5" {
		t.Fatalf("unexpected hosts: %v", hosts)
	}
}

func TestEffective_TenantDefaultLookupOmitsUserSegment(t *testing.T) {
	tenantID := uuid.New()

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenant_id":     tenantID,
			"user_id":       uuid.Nil,
			"allowed_hosts": []string{},
		})
	}))
	defer srv.Close()

	c := egressclient.New(srv.URL, "tok")
	if _, err := c.Effective(context.Background(), tenantID, uuid.Nil); err != nil {
		t.Fatalf("Effective: %v", err)
	}

	wantPath := "/internal/egress-policy/" + tenantID.String() + "/" + uuid.Nil.String()
	if gotPath != wantPath {
		t.Fatalf("expected path %q, got %q", wantPath, gotPath)
	}
}

func TestEffective_NonOKStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := egressclient.New(srv.URL, "tok")
	if _, err := c.Effective(context.Background(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestEffective_MalformedJSONIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := egressclient.New(srv.URL, "tok")
	if _, err := c.Effective(context.Background(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestEffective_UnreachableServerIsError(t *testing.T) {
	c := egressclient.New("http://127.0.0.1:1", "tok") // port 1: nothing listens, connection refused
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.Effective(ctx, uuid.New(), uuid.New()); err == nil {
		t.Fatal("expected error for unreachable control-plane")
	}
}
