package agenttask_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
)

// newTestHandler wires the agent engine as NotConfiguredEngine. It is the
// default for tests that never reach engine.Launch (request-shape
// validation), and for the dedicated unconfigured-engine coverage below.
// Tests that need a task to land somewhere non-terminal (running) use
// newRunningTestHandler instead.
//
// Both helpers return the Service alongside the Handler because create is now
// asynchronous over the launch (issue #881): any assertion about a launch
// outcome has to wait for it with Service.WaitForLaunches first.
func newTestHandler() (*agenttask.Handler, *agenttask.Service) {
	svc := agenttask.NewService(newFakeRepository(), agenttask.NotConfiguredEngine{})
	return agenttask.NewHandler(svc), svc
}

// newRunningTestHandler wires a fakeEngine that always launches
// successfully, so created tasks land in StatusRunning rather than
// immediately StatusFailed.
func newRunningTestHandler() (*agenttask.Handler, *agenttask.Service) {
	svc := agenttask.NewService(newFakeRepository(), &fakeEngine{sessionRef: "session-http-test"})
	return agenttask.NewHandler(svc), svc
}

// Create answers with the persisted queued task rather than waiting for the
// sandbox launch (issue #881). The caller learns the launch outcome from the
// same poll it already does for every later state change.
func TestHandler_Create_HappyPath(t *testing.T) {
	h, svc := newRunningTestHandler()
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

	// The launch still lands, it just lands on the read path.
	svc.WaitForLaunches()
	getW := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(getW, httptest.NewRequest(http.MethodGet,
		"/internal/agent-tasks/"+tenantID.String()+"/"+userID.String()+"/"+resp["id"].(string), nil))
	var got map[string]any
	if err := json.NewDecoder(getW.Body).Decode(&got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got["status"] != "running" {
		t.Errorf("expected the launched task to read as running, got %v", got["status"])
	}
}

// TestHandler_Create_EngineNotConfigured_FailsVisibly is the HTTP-layer
// guard for the bug report ("agents don't work, stuck in queue forever, no
// error surfaced"): a task submitted against an unconfigured engine must
// become one the caller can see is dead, not sit queued forever, and the
// error text must stay customer-safe. Since issue #881 the create response
// itself is the queued row and the failure arrives on the next read, which
// is the same path every other state change already travels.
func TestHandler_Create_EngineNotConfigured_FailsVisibly(t *testing.T) {
	h, svc := newTestHandler()
	tenantID, userID := uuid.New(), uuid.New()

	body, _ := json.Marshal(map[string]string{"pack": "coding-pack"})
	req := httptest.NewRequest(http.MethodPost, "/internal/agent-tasks/"+tenantID.String()+"/"+userID.String(), bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 (task row is still persisted), got %d: %s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	svc.WaitForLaunches()
	getW := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(getW, httptest.NewRequest(http.MethodGet,
		"/internal/agent-tasks/"+tenantID.String()+"/"+userID.String()+"/"+created["id"].(string), nil))
	var resp map[string]any
	if err := json.NewDecoder(getW.Body).Decode(&resp); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if resp["status"] != "failed" {
		t.Errorf("expected status failed (never queued forever), got %v", resp["status"])
	}
	errMsg, _ := resp["error_message"].(string)
	if errMsg == "" {
		t.Error("expected a non-empty error_message")
	}
	if strings.Contains(errMsg, "HIVE_AGENT_ENGINE") {
		t.Errorf("error_message leaked deployment/env detail to a customer-visible field: %q", errMsg)
	}
}

func TestHandler_Create_InvalidPack_Returns400(t *testing.T) {
	h, _ := newTestHandler()
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
	h, _ := newTestHandler()
	req := httptest.NewRequest(http.MethodPost, "/internal/agent-tasks/not-a-uuid/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandler_ListThenGet_RoundTrip(t *testing.T) {
	h, svc := newRunningTestHandler()
	tenantID, userID := uuid.New(), uuid.New()

	body, _ := json.Marshal(map[string]string{"pack": "knowledge-work-pack"})
	createReq := httptest.NewRequest(http.MethodPost, "/internal/agent-tasks/"+tenantID.String()+"/"+userID.String(), bytes.NewReader(body))
	createW := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(createW, createReq)
	var created map[string]any
	_ = json.NewDecoder(createW.Body).Decode(&created)
	taskID := created["id"].(string)
	svc.WaitForLaunches()

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
	h, _ := newTestHandler()
	tenantID, userID := uuid.New(), uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/internal/agent-tasks/"+tenantID.String()+"/"+userID.String()+"/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandler_Cancel_HappyPath(t *testing.T) {
	h, svc := newRunningTestHandler()
	tenantID, userID := uuid.New(), uuid.New()

	body, _ := json.Marshal(map[string]string{"pack": "coding-pack"})
	createReq := httptest.NewRequest(http.MethodPost, "/internal/agent-tasks/"+tenantID.String()+"/"+userID.String(), bytes.NewReader(body))
	createW := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(createW, createReq)
	var created map[string]any
	_ = json.NewDecoder(createW.Body).Decode(&created)
	taskID := created["id"].(string)
	// Cancel a task whose launch has landed, so this covers the running-task
	// path rather than racing the background launch.
	svc.WaitForLaunches()

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
	h, _ := newTestHandler()
	tenantID, userID := uuid.New(), uuid.New()
	req := httptest.NewRequest(http.MethodDelete, "/internal/agent-tasks/"+tenantID.String()+"/"+userID.String(), nil)
	w := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}
