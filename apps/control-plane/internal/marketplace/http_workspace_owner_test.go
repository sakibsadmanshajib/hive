package marketplace_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
)

// Issue #758. Choosing which catalog entries a workspace uses is workspace-
// scoped administration and belongs to the workspace OWNER, while curating the
// catalog itself stays a platform operation because public.marketplace_entries
// is one global table shared by every tenant.

const seedEntryBody = `{"kind":"mcp_server","name":"github","description":"GitHub MCP server","config":{"command":"npx"}}`

// seedEntry curates one catalog entry as a platform admin and returns its id.
func seedEntry(t *testing.T, h http.Handler) string {
	t.Helper()
	req := withViewer(httptest.NewRequest(http.MethodPost, "/api/v1/admin/marketplace", strings.NewReader(seedEntryBody)), adminViewer())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed entry: got %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created entryResp
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("seed entry decode: %v", err)
	}
	return created.ID
}

// The workspace owner reads the catalog. Before issue #758 this caller was
// refused by the platform-admin middleware and the console rendered a wall.
func TestAdmin_WorkspaceOwnerListsCatalog(t *testing.T) {
	h := newTestHandler().AdminMux()
	seedEntry(t, h)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withOwnerViewer(httptest.NewRequest(http.MethodGet, "/api/v1/admin/marketplace", nil), adminViewer()))

	if rec.Code != http.StatusOK {
		t.Fatalf("workspace owner got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Entries   []entryResp `json:"entries"`
		CanCurate bool        `json:"can_curate"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(resp.Entries))
	}
	if resp.CanCurate {
		t.Error("can_curate is true for a workspace owner, which would offer a control the API refuses")
	}
}

// Curating the shared catalog stays platform-only for every verb.
func TestAdmin_WorkspaceOwnerCannotCurateCatalog(t *testing.T) {
	h := newTestHandler().AdminMux()
	id := seedEntry(t, h)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create", http.MethodPost, "/api/v1/admin/marketplace", seedEntryBody},
		{"update", http.MethodPut, "/api/v1/admin/marketplace/" + id, `{"name":"renamed","description":"","config":{}}`},
		{"delete", http.MethodDelete, "/api/v1/admin/marketplace/" + id, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := withOwnerViewer(httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)), adminViewer())
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s by a workspace owner got %d, want 403: %s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}

// Enablement is the workspace decision, and it lands only on the tenant on the
// session. A second workspace still sees the entry disabled, which is the
// cross-workspace isolation this re-gating must not lose.
func TestAdmin_WorkspaceOwnerEnableIsScopedToOwnTenant(t *testing.T) {
	h := newTestHandler().AdminMux()
	id := seedEntry(t, h)

	ownerA := auth.Viewer{UserID: uuid.New(), TenantID: uuid.New()}
	ownerB := auth.Viewer{UserID: uuid.New(), TenantID: uuid.New()}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withOwnerViewer(
		httptest.NewRequest(http.MethodPut, "/api/v1/admin/marketplace/"+id+"/enable", strings.NewReader(`{"enabled":true}`)),
		ownerA,
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("workspace owner enable got %d, want 200: %s", rec.Code, rec.Body.String())
	}

	enabledFor := func(v auth.Viewer) bool {
		t.Helper()
		listRec := httptest.NewRecorder()
		h.ServeHTTP(listRec, withOwnerViewer(httptest.NewRequest(http.MethodGet, "/api/v1/admin/marketplace", nil), v))
		var resp struct {
			Entries []entryResp `json:"entries"`
		}
		if err := json.NewDecoder(listRec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Entries) != 1 {
			t.Fatalf("got %d entries, want 1", len(resp.Entries))
		}
		return resp.Entries[0].Enabled
	}

	if !enabledFor(ownerA) {
		t.Error("the enabling workspace does not see the entry enabled")
	}
	if enabledFor(ownerB) {
		t.Fatal("a second workspace sees the entry enabled: enablement leaked across tenants")
	}
}
