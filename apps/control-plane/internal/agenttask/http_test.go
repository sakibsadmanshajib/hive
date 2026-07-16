package agenttask_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
)

func newTestHandler() *agenttask.Handler {
	svc := agenttask.NewService(newFakeRepository(), agenttask.NotConfiguredEngine{})
	return agenttask.NewHandler(svc)
}

func TestHandler_Create_HappyPath(t *testing.T) {
	h := newTestHandler()
	tenantID, userID := uuid.New(), uuid.New()

	body, _ := json.Marshal(map[string]string{"pack": "coding-pack"})
	req := httptest.NewRequest(http.MethodPost, "/internal/agent-tasks/"+tenantID.String()+"/"+userID.String(), bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "queued" {
		t.Errorf("expected status queued, got %v", resp["status"])
	}
	if _, ok := resp["tenant_id"]; ok {
		t.Error("response must never echo tenant_id")
	}
}

func TestHandler_Create_InvalidPack_Returns400(t *testing.T) {
	h := newTestHandler()
	tenantID, userID := uuid.New(), uuid.New()

	body, _ := json.Marshal(map[string]string{"pack": "not-a-pack"})
	req := httptest.NewRequest(http.MethodPost, "/internal/agent-tasks/"+tenantID.String()+"/"+userID.String(), bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_Create_InvalidTenantID_Returns400(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodPost, "/internal/agent-tasks/not-a-uuid/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandler_ListThenGet_RoundTrip(t *testing.T) {
	h := newTestHandler()
	tenantID, userID := uuid.New(), uuid.New()

	body, _ := json.Marshal(map[string]string{"pack": "knowledge-work-pack"})
	createReq := httptest.NewRequest(http.MethodPost, "/internal/agent-tasks/"+tenantID.String()+"/"+userID.String(), bytes.NewReader(body))
	createW := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(createW, createReq)
	var created map[string]any
	_ = json.NewDecoder(createW.Body).Decode(&created)
	taskID := created["id"].(string)

	listReq := httptest.NewRequest(http.MethodGet, "/internal/agent-tasks/"+tenantID.String()+"/"+userID.String(), nil)
	listW := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("expected 200 on list, got %d: %s", listW.Code, listW.Body.String())
	}
	var listResp struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.NewDecoder(listW.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Tasks) != 1 {
		t.Fatalf("expected 1 task in list, got %d", len(listResp.Tasks))
	}

	getReq := httptest.NewRequest(http.MethodGet, "/internal/agent-tasks/"+tenantID.String()+"/"+userID.String()+"/"+taskID, nil)
	getW := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("expected 200 on get, got %d: %s", getW.Code, getW.Body.String())
	}
}

func TestHandler_Get_UnknownTask_Returns404(t *testing.T) {
	h := newTestHandler()
	tenantID, userID := uuid.New(), uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/internal/agent-tasks/"+tenantID.String()+"/"+userID.String()+"/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandler_Cancel_HappyPath(t *testing.T) {
	h := newTestHandler()
	tenantID, userID := uuid.New(), uuid.New()

	body, _ := json.Marshal(map[string]string{"pack": "coding-pack"})
	createReq := httptest.NewRequest(http.MethodPost, "/internal/agent-tasks/"+tenantID.String()+"/"+userID.String(), bytes.NewReader(body))
	createW := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(createW, createReq)
	var created map[string]any
	_ = json.NewDecoder(createW.Body).Decode(&created)
	taskID := created["id"].(string)

	cancelReq := httptest.NewRequest(http.MethodPost, "/internal/agent-tasks/"+tenantID.String()+"/"+userID.String()+"/"+taskID+"/cancel", nil)
	cancelW := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(cancelW, cancelReq)
	if cancelW.Code != http.StatusOK {
		t.Fatalf("expected 200 on cancel, got %d: %s", cancelW.Code, cancelW.Body.String())
	}
	var cancelled map[string]any
	_ = json.NewDecoder(cancelW.Body).Decode(&cancelled)
	if cancelled["status"] != "cancelled" {
		t.Errorf("expected status cancelled, got %v", cancelled["status"])
	}

	// A second cancel on an already-terminal task is a conflict, not a silent 200.
	secondW := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(secondW, httptest.NewRequest(http.MethodPost, "/internal/agent-tasks/"+tenantID.String()+"/"+userID.String()+"/"+taskID+"/cancel", nil))
	if secondW.Code != http.StatusConflict {
		t.Errorf("expected 409 on double-cancel, got %d", secondW.Code)
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	h := newTestHandler()
	tenantID, userID := uuid.New(), uuid.New()
	req := httptest.NewRequest(http.MethodDelete, "/internal/agent-tasks/"+tenantID.String()+"/"+userID.String(), nil)
	w := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}
