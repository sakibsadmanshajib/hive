package agenttask

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

type fakeClient struct {
	tasks map[uuid.UUID]Task

	createErr error
	getErr    error
	cancelErr error
}

func newFakeClient() *fakeClient {
	return &fakeClient{tasks: make(map[uuid.UUID]Task)}
}

func (f *fakeClient) Create(_ context.Context, _, _ uuid.UUID, pack, instructions string) (Task, error) {
	if f.createErr != nil {
		return Task{}, f.createErr
	}
	id := uuid.New()
	t := Task{ID: id.String(), Pack: pack, Instructions: instructions, Status: "queued"}
	f.tasks[id] = t
	return t, nil
}

func (f *fakeClient) List(context.Context, uuid.UUID, uuid.UUID) ([]Task, error) {
	out := make([]Task, 0, len(f.tasks))
	for _, t := range f.tasks {
		out = append(out, t)
	}
	return out, nil
}

func (f *fakeClient) Get(_ context.Context, _, _, taskID uuid.UUID) (Task, error) {
	if f.getErr != nil {
		return Task{}, f.getErr
	}
	t, ok := f.tasks[taskID]
	if !ok {
		return Task{}, ErrNotFound
	}
	return t, nil
}

func (f *fakeClient) Cancel(_ context.Context, _, _, taskID uuid.UUID) (Task, error) {
	if f.cancelErr != nil {
		return Task{}, f.cancelErr
	}
	t, ok := f.tasks[taskID]
	if !ok {
		return Task{}, ErrNotFound
	}
	t.Status = "cancelled"
	f.tasks[taskID] = t
	return t, nil
}

func userCtx(tenantID uuid.UUID) context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: uuid.New(), TenantID: tenantID})
}

func TestHandleCreate_HappyPath(t *testing.T) {
	h := NewHandler(newFakeClient())
	body, _ := json.Marshal(createTaskRequest{Pack: "coding-pack"})
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/tasks", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.routeTasks(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCreate_Unauthenticated(t *testing.T) {
	h := NewHandler(newFakeClient())
	body, _ := json.Marshal(createTaskRequest{Pack: "coding-pack"})
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/tasks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.routeTasks(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleCreate_MissingPack(t *testing.T) {
	h := NewHandler(newFakeClient())
	body, _ := json.Marshal(createTaskRequest{})
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/tasks", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.routeTasks(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleList_HappyPath(t *testing.T) {
	client := newFakeClient()
	h := NewHandler(client)
	tenantID := uuid.New()

	createReq := httptest.NewRequest(http.MethodPost, "/v1/agent/tasks", bytes.NewReader(mustJSON(createTaskRequest{Pack: "coding-pack"})))
	createReq = createReq.WithContext(userCtx(tenantID))
	h.routeTasks(httptest.NewRecorder(), createReq)

	listReq := httptest.NewRequest(http.MethodGet, "/v1/agent/tasks", nil)
	listReq = listReq.WithContext(userCtx(tenantID))
	w := httptest.NewRecorder()
	h.routeTasks(w, listReq)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Tasks []Task `json:"tasks"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(resp.Tasks))
	}
}

func TestHandleGet_NotFound_Returns404(t *testing.T) {
	h := NewHandler(newFakeClient())
	taskID := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/agent/tasks/"+taskID.String(), nil)
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.routeTaskByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleCancel_HappyPath(t *testing.T) {
	client := newFakeClient()
	h := NewHandler(client)
	tenantID := uuid.New()

	createW := httptest.NewRecorder()
	createReq := httptest.NewRequest(http.MethodPost, "/v1/agent/tasks", bytes.NewReader(mustJSON(createTaskRequest{Pack: "coding-pack"})))
	createReq = createReq.WithContext(userCtx(tenantID))
	h.routeTasks(createW, createReq)
	var created Task
	_ = json.NewDecoder(createW.Body).Decode(&created)

	cancelReq := httptest.NewRequest(http.MethodPost, "/v1/agent/tasks/"+created.ID+"/cancel", nil)
	cancelReq = cancelReq.WithContext(userCtx(tenantID))
	w := httptest.NewRecorder()
	h.routeTaskByID(w, cancelReq)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var cancelled Task
	_ = json.NewDecoder(w.Body).Decode(&cancelled)
	if cancelled.Status != "cancelled" {
		t.Errorf("expected status cancelled, got %q", cancelled.Status)
	}
}

func TestHandleCancel_TerminalStateReturns409(t *testing.T) {
	client := newFakeClient()
	client.cancelErr = ErrTerminalState
	h := NewHandler(client)

	req := httptest.NewRequest(http.MethodPost, "/v1/agent/tasks/"+uuid.New().String()+"/cancel", nil)
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	h.routeTaskByID(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

func TestExtractTaskPath_Valid(t *testing.T) {
	id := uuid.New()
	gotID, cancel, err := extractTaskPath("/v1/agent/tasks/" + id.String())
	if err != nil || gotID != id || cancel {
		t.Errorf("extractTaskPath failed: id=%v cancel=%v err=%v", gotID, cancel, err)
	}
}

func TestExtractTaskPath_Cancel(t *testing.T) {
	id := uuid.New()
	gotID, cancel, err := extractTaskPath("/v1/agent/tasks/" + id.String() + "/cancel")
	if err != nil || gotID != id || !cancel {
		t.Errorf("extractTaskPath cancel failed: id=%v cancel=%v err=%v", gotID, cancel, err)
	}
}

func TestExtractTaskPath_Invalid(t *testing.T) {
	if _, _, err := extractTaskPath("/v1/agent/tasks/not-a-uuid"); err == nil {
		t.Error("expected error for invalid UUID")
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
