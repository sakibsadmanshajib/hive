package marketplace_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/marketplace"
)

func adminViewer() auth.Viewer {
	return auth.Viewer{UserID: uuid.New(), TenantID: uuid.New()}
}

func withViewer(req *http.Request, v auth.Viewer) *http.Request {
	return req.WithContext(auth.WithViewer(req.Context(), v))
}

func newTestHandler() *marketplace.Handler {
	return marketplace.NewHandler(marketplace.NewService(newFakeRepository()))
}

func TestAdmin_List_Unauthenticated(t *testing.T) {
	h := newTestHandler().AdminMux()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/marketplace", nil) // no viewer in context
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAdmin_List_NoTenantSelected(t *testing.T) {
	h := newTestHandler().AdminMux()
	req := withViewer(
		httptest.NewRequest(http.MethodGet, "/api/v1/admin/marketplace", nil),
		auth.Viewer{UserID: uuid.New()}, // TenantID is uuid.Nil
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestAdmin_List_Empty(t *testing.T) {
	h := newTestHandler().AdminMux()
	req := withViewer(httptest.NewRequest(http.MethodGet, "/api/v1/admin/marketplace", nil), adminViewer())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Entries == nil {
		t.Error("expected entries to serialize as [] not null")
	}
	if len(resp.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(resp.Entries))
	}
}

type entryResp struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Config      json.RawMessage `json:"config"`
	Enabled     bool            `json:"enabled"`
}

func TestAdmin_Create_ThenList_ShowsDisabledEntry(t *testing.T) {
	h := newTestHandler().AdminMux()
	v := adminViewer()

	createBody := `{"kind":"mcp_server","name":"github","description":"GitHub MCP server","config":{"command":"npx","args":["-y","@modelcontextprotocol/server-github"]}}`
	req := withViewer(httptest.NewRequest(http.MethodPost, "/api/v1/admin/marketplace", strings.NewReader(createBody)), v)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created entryResp
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Kind != "mcp_server" || created.Name != "github" || created.Enabled {
		t.Errorf("created entry = %+v, want kind=mcp_server name=github enabled=false", created)
	}

	listReq := withViewer(httptest.NewRequest(http.MethodGet, "/api/v1/admin/marketplace", nil), v)
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, listReq)
	var listResp struct {
		Entries []entryResp `json:"entries"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Entries) != 1 || listResp.Entries[0].ID != created.ID {
		t.Fatalf("expected the created entry to appear in the list, got %+v", listResp.Entries)
	}
}

func TestAdmin_Create_InvalidKind(t *testing.T) {
	h := newTestHandler().AdminMux()
	req := withViewer(
		httptest.NewRequest(http.MethodPost, "/api/v1/admin/marketplace", strings.NewReader(`{"kind":"not_a_kind","name":"x"}`)),
		adminViewer(),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdmin_Create_InvalidBody(t *testing.T) {
	h := newTestHandler().AdminMux()
	req := withViewer(
		httptest.NewRequest(http.MethodPost, "/api/v1/admin/marketplace", strings.NewReader("not json")),
		adminViewer(),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestAdmin_Update_UnknownEntry(t *testing.T) {
	h := newTestHandler().AdminMux()
	req := withViewer(
		httptest.NewRequest(http.MethodPut, "/api/v1/admin/marketplace/"+uuid.New().String(), strings.NewReader(`{"name":"x"}`)),
		adminViewer(),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdmin_Delete_ThenEnable_Fails(t *testing.T) {
	h := newTestHandler().AdminMux()
	v := adminViewer()

	createReq := withViewer(
		httptest.NewRequest(http.MethodPost, "/api/v1/admin/marketplace", strings.NewReader(`{"kind":"skill","name":"deck-writer","config":{}}`)),
		v,
	)
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	var created entryResp
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	delReq := withViewer(httptest.NewRequest(http.MethodDelete, "/api/v1/admin/marketplace/"+created.ID, nil), v)
	delRec := httptest.NewRecorder()
	h.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on delete, got %d", delRec.Code)
	}

	enableReq := withViewer(
		httptest.NewRequest(http.MethodPut, "/api/v1/admin/marketplace/"+created.ID+"/enable", strings.NewReader(`{"enabled":true}`)),
		v,
	)
	enableRec := httptest.NewRecorder()
	h.ServeHTTP(enableRec, enableReq)
	if enableRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 enabling a deleted entry, got %d", enableRec.Code)
	}
}

func TestAdmin_SetEnabled_RoundTrip(t *testing.T) {
	h := newTestHandler().AdminMux()
	v := adminViewer()

	createReq := withViewer(
		httptest.NewRequest(http.MethodPost, "/api/v1/admin/marketplace", strings.NewReader(`{"kind":"mcp_server","name":"github","config":{"command":"npx"}}`)),
		v,
	)
	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, createReq)
	var created entryResp
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	enableReq := withViewer(
		httptest.NewRequest(http.MethodPut, "/api/v1/admin/marketplace/"+created.ID+"/enable", strings.NewReader(`{"enabled":true}`)),
		v,
	)
	enableRec := httptest.NewRecorder()
	h.ServeHTTP(enableRec, enableReq)
	if enableRec.Code != http.StatusOK {
		t.Fatalf("expected 200 enabling, got %d: %s", enableRec.Code, enableRec.Body.String())
	}

	listReq := withViewer(httptest.NewRequest(http.MethodGet, "/api/v1/admin/marketplace", nil), v)
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, listReq)
	var listResp struct {
		Entries []entryResp `json:"entries"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Entries) != 1 || !listResp.Entries[0].Enabled {
		t.Fatalf("expected the entry to show enabled=true, got %+v", listResp.Entries)
	}

	// A different tenant viewer must not see it enabled — enablement is per tenant.
	otherRec := httptest.NewRecorder()
	h.ServeHTTP(otherRec, withViewer(httptest.NewRequest(http.MethodGet, "/api/v1/admin/marketplace", nil), adminViewer()))
	var otherResp struct {
		Entries []entryResp `json:"entries"`
	}
	if err := json.NewDecoder(otherRec.Body).Decode(&otherResp); err != nil {
		t.Fatalf("decode other tenant list: %v", err)
	}
	if len(otherResp.Entries) != 1 || otherResp.Entries[0].Enabled {
		t.Fatalf("expected the entry to show enabled=false for an unrelated tenant, got %+v", otherResp.Entries)
	}
}

func TestAdmin_MethodNotAllowed(t *testing.T) {
	h := newTestHandler().AdminMux()
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPatch, "/api/v1/admin/marketplace"},
		{http.MethodGet, "/api/v1/admin/marketplace/" + uuid.New().String()},
	}
	for _, c := range cases {
		req := withViewer(httptest.NewRequest(c.method, c.path, nil), adminViewer())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: expected 405, got %d", c.method, c.path, rec.Code)
		}
	}
}

func TestInternal_MCPServers_OnlyEnabledMCPKind(t *testing.T) {
	repo := newFakeRepository()
	svc := marketplace.NewService(repo)
	h := marketplace.NewHandler(svc).InternalMux()
	ctx := context.Background()
	tenantID, actorID := uuid.New(), uuid.New()

	mcp, err := svc.CreateEntry(ctx, marketplace.KindMCPServer, "github", "", json.RawMessage(`{"command":"npx","args":["-y","server-github"]}`), uuid.New())
	if err != nil {
		t.Fatalf("seed mcp: %v", err)
	}
	skill, err := svc.CreateEntry(ctx, marketplace.KindSkill, "deck-writer", "", nil, uuid.New())
	if err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	if err := svc.SetEnabled(ctx, tenantID, mcp.ID, true, actorID); err != nil {
		t.Fatalf("enable mcp: %v", err)
	}
	if err := svc.SetEnabled(ctx, tenantID, skill.ID, true, actorID); err != nil {
		t.Fatalf("enable skill: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/internal/marketplace/"+tenantID.String()+"/mcp-servers", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		TenantID string `json:"tenant_id"`
		Servers  []struct {
			Name   string          `json:"name"`
			Config json.RawMessage `json:"config"`
		} `json:"servers"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TenantID != tenantID.String() {
		t.Errorf("tenant_id = %q, want %q", resp.TenantID, tenantID.String())
	}
	if len(resp.Servers) != 1 || resp.Servers[0].Name != "github" {
		t.Fatalf("expected exactly the enabled github mcp_server entry, got %+v", resp.Servers)
	}
}

func TestInternal_MCPServers_InvalidTenantID(t *testing.T) {
	h := newTestHandler().InternalMux()
	req := httptest.NewRequest(http.MethodGet, "/internal/marketplace/not-a-uuid/mcp-servers", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestInternal_MethodNotAllowed(t *testing.T) {
	h := newTestHandler().InternalMux()
	req := httptest.NewRequest(http.MethodPost, "/internal/marketplace/"+uuid.New().String()+"/mcp-servers", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}
