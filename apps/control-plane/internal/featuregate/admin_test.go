package featuregate_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/featuregate"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenant/settings"
)

// fakeAdminStore is a hand-built AdminStore stub. It records the last Set call
// so tests can assert the handler forwarded tenant, key, enabled, and the
// attributed user id unchanged.
type fakeAdminStore struct {
	registry []settings.GateKey
	enabled  map[settings.Key]bool
	setErr   error

	setCalled  bool
	setTenant  uuid.UUID
	setKey     settings.Key
	setEnabled bool
	setBy      uuid.UUID
}

func (f *fakeAdminStore) Registry(context.Context) ([]settings.GateKey, error) {
	return f.registry, nil
}

func (f *fakeAdminStore) AllEnabled(context.Context, uuid.UUID) (map[settings.Key]bool, error) {
	return f.enabled, nil
}

func (f *fakeAdminStore) Set(_ context.Context, tenantID uuid.UUID, key settings.Key, enabled bool, updatedBy uuid.UUID) error {
	f.setCalled = true
	f.setTenant = tenantID
	f.setKey = key
	f.setEnabled = enabled
	f.setBy = updatedBy
	return f.setErr
}

type gateRow struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Category string `json:"category"`
	Enabled  bool   `json:"enabled"`
}

type gatesResp struct {
	Gates []gateRow `json:"gates"`
}

func adminViewer() auth.Viewer {
	return auth.Viewer{UserID: uuid.New(), TenantID: uuid.New()}
}

func withViewer(req *http.Request, v auth.Viewer) *http.Request {
	return req.WithContext(auth.WithViewer(req.Context(), v))
}

func TestAdmin_List_MergesRegistryAndEnablement(t *testing.T) {
	store := &fakeAdminStore{
		registry: []settings.GateKey{
			{Key: settings.EnableRAG, Label: "Carl.sh RAG capability", Category: "carl"},
			{Key: settings.EnablePublicBilling, Label: "Public billing", Category: "billing"},
		},
		enabled: map[settings.Key]bool{settings.EnableRAG: true},
	}
	h := featuregate.NewAdminHandler(store).AdminMux()

	v := adminViewer()
	req := withViewer(httptest.NewRequest(http.MethodGet, "/api/v1/admin/feature-gates", nil), v)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp gatesResp
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Gates) != 2 {
		t.Fatalf("expected 2 gates, got %d", len(resp.Gates))
	}
	// Registry order is preserved: RAG first, billing second.
	if resp.Gates[0].Key != string(settings.EnableRAG) || !resp.Gates[0].Enabled {
		t.Errorf("gate[0] = %+v, want RAG enabled", resp.Gates[0])
	}
	if resp.Gates[0].Label != "Carl.sh RAG capability" || resp.Gates[0].Category != "carl" {
		t.Errorf("gate[0] label/category = %q/%q", resp.Gates[0].Label, resp.Gates[0].Category)
	}
	// A registered key with no tenant_settings row defaults to disabled.
	if resp.Gates[1].Key != string(settings.EnablePublicBilling) || resp.Gates[1].Enabled {
		t.Errorf("gate[1] = %+v, want billing disabled", resp.Gates[1])
	}
}

func TestAdmin_List_Unauthenticated(t *testing.T) {
	h := featuregate.NewAdminHandler(&fakeAdminStore{}).AdminMux()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/feature-gates", nil) // no viewer in context
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAdmin_List_NoTenantSelected(t *testing.T) {
	h := featuregate.NewAdminHandler(&fakeAdminStore{}).AdminMux()
	req := withViewer(
		httptest.NewRequest(http.MethodGet, "/api/v1/admin/feature-gates", nil),
		auth.Viewer{UserID: uuid.New()}, // TenantID is uuid.Nil
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestAdmin_Set_TogglesGate(t *testing.T) {
	store := &fakeAdminStore{}
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
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !store.setCalled {
		t.Fatal("Set was not called")
	}
	if store.setTenant != v.TenantID || store.setBy != v.UserID {
		t.Errorf("Set attribution: tenant=%v by=%v, want tenant=%v by=%v", store.setTenant, store.setBy, v.TenantID, v.UserID)
	}
	if store.setKey != settings.EnableRAG || !store.setEnabled {
		t.Errorf("Set(key=%q, enabled=%v), want (ENABLE_RAG, true)", store.setKey, store.setEnabled)
	}
	var resp setResp
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Key != "ENABLE_RAG" || !resp.Enabled {
		t.Errorf("response = %+v, want ENABLE_RAG enabled", resp)
	}
}

type setResp struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

func TestAdmin_Set_UnknownKey(t *testing.T) {
	store := &fakeAdminStore{setErr: settings.ErrUnknownGateKey}
	h := featuregate.NewAdminHandler(store).AdminMux()

	req := withViewer(
		httptest.NewRequest(http.MethodPut, "/api/v1/admin/feature-gates/NOT_A_REAL_KEY",
			strings.NewReader(`{"enabled":true}`)),
		adminViewer(),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown key, got %d", rec.Code)
	}
}

func TestAdmin_Set_InvalidBody(t *testing.T) {
	h := featuregate.NewAdminHandler(&fakeAdminStore{}).AdminMux()
	req := withViewer(
		httptest.NewRequest(http.MethodPut, "/api/v1/admin/feature-gates/ENABLE_RAG",
			strings.NewReader("not json")),
		adminViewer(),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid body, got %d", rec.Code)
	}
}

func TestAdmin_MethodNotAllowed(t *testing.T) {
	h := featuregate.NewAdminHandler(&fakeAdminStore{}).AdminMux()
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/admin/feature-gates"},
		{http.MethodDelete, "/api/v1/admin/feature-gates/ENABLE_RAG"},
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
