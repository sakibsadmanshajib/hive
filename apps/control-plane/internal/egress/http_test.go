package egress_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/egress"
)

func withViewer(r *http.Request, userID uuid.UUID) *http.Request {
	ctx := auth.WithViewer(r.Context(), auth.Viewer{UserID: userID, EmailVerified: true})
	return r.WithContext(ctx)
}

func TestHandler_AdminPutTenantDefault_OwnerSetsHosts(t *testing.T) {
	repo := newFakeRepo()
	callerID, tenantID := uuid.New(), uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})
	h := egress.NewHandler(svc)

	body := `{"allowed_hosts":["pypi.org","github.com"]}`
	req := withViewer(httptest.NewRequest(http.MethodPut, "/api/v1/egress-policy/"+tenantID.String(), strings.NewReader(body)), callerID)
	rec := httptest.NewRecorder()
	h.AdminMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		AllowedHosts []string `json:"allowed_hosts"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.AllowedHosts) != 2 {
		t.Fatalf("expected 2 hosts, got %v", resp.AllowedHosts)
	}
}

func TestHandler_AdminPutTenantDefault_NonOwnerForbidden(t *testing.T) {
	repo := newFakeRepo()
	callerID, tenantID := uuid.New(), uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: false})
	h := egress.NewHandler(svc)

	body := `{"allowed_hosts":["pypi.org"]}`
	req := withViewer(httptest.NewRequest(http.MethodPut, "/api/v1/egress-policy/"+tenantID.String(), strings.NewReader(body)), callerID)
	rec := httptest.NewRecorder()
	h.AdminMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestHandler_AdminPutTenantDefault_Unauthenticated_Returns401(t *testing.T) {
	repo := newFakeRepo()
	tenantID := uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})
	h := egress.NewHandler(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/egress-policy/"+tenantID.String(), strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.AdminMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandler_AdminPutTenantDefault_InvalidJSON_Returns400(t *testing.T) {
	repo := newFakeRepo()
	callerID, tenantID := uuid.New(), uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})
	h := egress.NewHandler(svc)

	req := withViewer(httptest.NewRequest(http.MethodPut, "/api/v1/egress-policy/"+tenantID.String(), strings.NewReader("not json")), callerID)
	rec := httptest.NewRecorder()
	h.AdminMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandler_AdminGetTenantDefault_InvalidTenantID_Returns400(t *testing.T) {
	repo := newFakeRepo()
	callerID := uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})
	h := egress.NewHandler(svc)

	req := withViewer(httptest.NewRequest(http.MethodGet, "/api/v1/egress-policy/not-a-uuid", nil), callerID)
	rec := httptest.NewRecorder()
	h.AdminMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandler_AdminGetTenantDefault_NotFound(t *testing.T) {
	repo := newFakeRepo()
	callerID, tenantID := uuid.New(), uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})
	h := egress.NewHandler(svc)

	req := withViewer(httptest.NewRequest(http.MethodGet, "/api/v1/egress-policy/"+tenantID.String(), nil), callerID)
	rec := httptest.NewRecorder()
	h.AdminMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandler_AdminUserOverride_PutGetDelete(t *testing.T) {
	repo := newFakeRepo()
	callerID, tenantID, targetUserID := uuid.New(), uuid.New(), uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})
	h := egress.NewHandler(svc)
	base := "/api/v1/egress-policy/" + tenantID.String() + "/users/" + targetUserID.String()

	putReq := withViewer(httptest.NewRequest(http.MethodPut, base, strings.NewReader(`{"allowed_hosts":["docs.python.org"]}`)), callerID)
	putRec := httptest.NewRecorder()
	h.AdminMux().ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", putRec.Code, putRec.Body.String())
	}

	getReq := withViewer(httptest.NewRequest(http.MethodGet, base, nil), callerID)
	getRec := httptest.NewRecorder()
	h.AdminMux().ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d", getRec.Code)
	}
	var resp struct {
		UserID       string   `json:"user_id"`
		AllowedHosts []string `json:"allowed_hosts"`
	}
	if err := json.NewDecoder(getRec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.UserID != targetUserID.String() || len(resp.AllowedHosts) != 1 {
		t.Fatalf("unexpected override body: %+v", resp)
	}

	delReq := withViewer(httptest.NewRequest(http.MethodDelete, base, nil), callerID)
	delRec := httptest.NewRecorder()
	h.AdminMux().ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("DELETE expected 200, got %d", delRec.Code)
	}

	getAfterDelete := withViewer(httptest.NewRequest(http.MethodGet, base, nil), callerID)
	getAfterRec := httptest.NewRecorder()
	h.AdminMux().ServeHTTP(getAfterRec, getAfterDelete)
	if getAfterRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", getAfterRec.Code)
	}
}

func TestHandler_InternalEffective_ReturnsAllowedHostsShape(t *testing.T) {
	repo := newFakeRepo()
	tenantID, userID := uuid.New(), uuid.New()
	repo.overrides[[2]uuid.UUID{tenantID, userID}] = egress.Policy{TenantID: tenantID, UserID: userID, AllowedHosts: []string{"pypi.org"}}
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})
	h := egress.NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/internal/egress-policy/"+tenantID.String()+"/"+userID.String(), nil)
	rec := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		TenantID     string   `json:"tenant_id"`
		UserID       string   `json:"user_id"`
		AllowedHosts []string `json:"allowed_hosts"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TenantID != tenantID.String() || resp.UserID != userID.String() {
		t.Fatalf("unexpected scope echoed back: %+v", resp)
	}
	if len(resp.AllowedHosts) != 1 || resp.AllowedHosts[0] != "pypi.org" {
		t.Fatalf("expected [pypi.org], got %v", resp.AllowedHosts)
	}
}

func TestHandler_InternalEffective_TenantOnly_NoUserSegment(t *testing.T) {
	repo := newFakeRepo()
	tenantID := uuid.New()
	repo.defaults[tenantID] = egress.Policy{TenantID: tenantID, AllowedHosts: []string{"github.com"}}
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})
	h := egress.NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/internal/egress-policy/"+tenantID.String(), nil)
	rec := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		AllowedHosts []string `json:"allowed_hosts"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.AllowedHosts) != 1 || resp.AllowedHosts[0] != "github.com" {
		t.Fatalf("expected [github.com], got %v", resp.AllowedHosts)
	}
}

func TestHandler_InternalEffective_NothingSet_ReturnsEmptyArrayNotNull(t *testing.T) {
	repo := newFakeRepo()
	tenantID, userID := uuid.New(), uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})
	h := egress.NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/internal/egress-policy/"+tenantID.String()+"/"+userID.String(), nil)
	rec := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"allowed_hosts":null`) {
		t.Fatalf("expected [] not null in body: %s", rec.Body.String())
	}
}

func TestHandler_InternalEffective_InvalidTenantID_Returns400(t *testing.T) {
	repo := newFakeRepo()
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})
	h := egress.NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/internal/egress-policy/not-a-uuid", nil)
	rec := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandler_InternalEffective_MethodNotAllowed(t *testing.T) {
	repo := newFakeRepo()
	tenantID := uuid.New()
	svc := egress.NewService(repo, &fakeOwner{isOwner: true})
	h := egress.NewHandler(svc)

	req := httptest.NewRequest(http.MethodPost, "/internal/egress-policy/"+tenantID.String(), nil)
	rec := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}
