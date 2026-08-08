package featuregate_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/featuregate"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenant/settings"
)

// Issue #758. The workspace OWNER administers the feature gates of the
// workspace, and platform.WorkspaceAdminGate stamps a false platform-admin
// overlay for that caller. The gates that are not a customer decision (billing
// entitlements, deployment shape) stay platform-only inside this handler.

// withAdminViewer authenticates as a platform admin, which is what the gate
// stamps for a platform operator.
func withAdminViewer(req *http.Request, v auth.Viewer) *http.Request {
	ctx := platform.WithPlatformAdmin(req.Context(), true)
	return req.WithContext(auth.WithViewer(ctx, v))
}

// workspaceRegistry pairs one workspace-scoped gate with one platform-managed
// gate, so every test below can assert both postures from one fixture.
func workspaceRegistry() []settings.GateKey {
	return []settings.GateKey{
		{Key: settings.EnableRAG, Label: "Workspace RAG capability", Category: "carl"},
		{Key: settings.EnableExtraUsage, Label: "Extra usage beyond plan", Category: "billing"},
	}
}

// The owner of the workspace reaches the surface and can flip a gate that
// belongs to the workspace. Before issue #758 this caller got a flat 403 from
// the platform-admin middleware and never reached the handler at all.
func TestAdmin_WorkspaceOwnerTogglesWorkspaceGate(t *testing.T) {
	store := &fakeAdminStore{registry: workspaceRegistry()}
	h := featuregate.NewAdminHandler(store).AdminMux()

	v := adminViewer()
	req := withViewer(
		httptest.NewRequest(http.MethodPut, "/api/v1/admin/feature-gates/ENABLE_RAG",
			strings.NewReader(`{"enabled":true}`)),
		v,
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("workspace owner got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !store.setCalled {
		t.Fatal("workspace owner toggle did not reach the store")
	}
	if store.setTenant != v.TenantID {
		t.Errorf("write landed on tenant %v, want the selected tenant %v", store.setTenant, v.TenantID)
	}
}

// Cross-workspace isolation at the handler. The tenant written is the one on
// the session, never one named by the request, so a caller cannot aim a toggle
// at somebody else workspace by hand-crafting a body.
func TestAdmin_ToggleIgnoresTenantInRequestBody(t *testing.T) {
	store := &fakeAdminStore{registry: workspaceRegistry()}
	h := featuregate.NewAdminHandler(store).AdminMux()

	v := adminViewer()
	otherTenant := uuid.New()
	body := `{"enabled":true,"tenant_id":"` + otherTenant.String() + `"}`
	req := withViewer(
		httptest.NewRequest(http.MethodPut, "/api/v1/admin/feature-gates/ENABLE_RAG", strings.NewReader(body)),
		v,
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if store.setTenant == otherTenant {
		t.Fatal("a tenant id in the request body redirected the write across workspaces")
	}
	if store.setTenant != v.TenantID {
		t.Errorf("write landed on tenant %v, want %v", store.setTenant, v.TenantID)
	}
}

// A billing-category gate is a plan entitlement, so the workspace owner may see
// it but not grant it to itself.
func TestAdmin_WorkspaceOwnerCannotToggleBillingGate(t *testing.T) {
	store := &fakeAdminStore{registry: workspaceRegistry()}
	h := featuregate.NewAdminHandler(store).AdminMux()

	req := withViewer(
		httptest.NewRequest(http.MethodPut, "/api/v1/admin/feature-gates/ENABLE_EXTRA_USAGE",
			strings.NewReader(`{"enabled":true}`)),
		adminViewer(),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("workspace owner got %d on a billing gate, want 403", rec.Code)
	}
	if store.setCalled {
		t.Fatal("a billing gate was written for a caller who is not a platform admin")
	}
}

// The platform operator keeps the platform-managed gates.
func TestAdmin_PlatformAdminTogglesBillingGate(t *testing.T) {
	store := &fakeAdminStore{registry: workspaceRegistry()}
	h := featuregate.NewAdminHandler(store).AdminMux()

	req := withAdminViewer(
		httptest.NewRequest(http.MethodPut, "/api/v1/admin/feature-gates/ENABLE_EXTRA_USAGE",
			strings.NewReader(`{"enabled":true}`)),
		adminViewer(),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("platform admin got %d on a billing gate, want 200", rec.Code)
	}
	if !store.setCalled {
		t.Fatal("platform admin toggle did not reach the store")
	}
}

type manageableRow struct {
	Key        string `json:"key"`
	Manageable bool   `json:"manageable"`
}

type manageableResp struct {
	Gates []manageableRow `json:"gates"`
}

func manageableByKey(t *testing.T, rec *httptest.ResponseRecorder) map[string]bool {
	t.Helper()
	var resp manageableResp
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := make(map[string]bool, len(resp.Gates))
	for _, g := range resp.Gates {
		out[g.Key] = g.Manageable
	}
	return out
}

// The list tells the console which rows this caller may change, so it can render
// the platform-managed rows read-only rather than offering a control that would
// be refused.
func TestAdmin_ListMarksPlatformManagedGatesUnmanageable(t *testing.T) {
	store := &fakeAdminStore{registry: workspaceRegistry()}
	h := featuregate.NewAdminHandler(store).AdminMux()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withViewer(httptest.NewRequest(http.MethodGet, "/api/v1/admin/feature-gates", nil), adminViewer()))
	if rec.Code != http.StatusOK {
		t.Fatalf("workspace owner list got %d, want 200", rec.Code)
	}
	owner := manageableByKey(t, rec)
	if !owner["ENABLE_RAG"] {
		t.Error("workspace gate is not manageable by the workspace owner")
	}
	if owner["ENABLE_EXTRA_USAGE"] {
		t.Error("billing gate is marked manageable by the workspace owner")
	}

	recAdmin := httptest.NewRecorder()
	h.ServeHTTP(recAdmin, withAdminViewer(httptest.NewRequest(http.MethodGet, "/api/v1/admin/feature-gates", nil), adminViewer()))
	admin := manageableByKey(t, recAdmin)
	if !admin["ENABLE_EXTRA_USAGE"] {
		t.Error("billing gate is not manageable by a platform admin")
	}
}
