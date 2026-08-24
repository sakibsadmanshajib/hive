package agenttask

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

type fakeClient struct {
	tasks map[uuid.UUID]Task

	createErr error
	getErr    error
	cancelErr error

	// lastBearerJWT records what handleCreate passed through, so a test can
	// assert on it without a real control-plane.
	lastBearerJWT string
	eventsFn      func(taskID uuid.UUID, afterSeq int64, limit int) ([]Event, error)
	filesFn       func(taskID uuid.UUID) ([]WorkspaceFile, error)
}

func newFakeClient() *fakeClient {
	return &fakeClient{tasks: make(map[uuid.UUID]Task)}
}

func (f *fakeClient) Create(_ context.Context, _, _ uuid.UUID, pack, instructions, bearerJWT string) (Task, error) {
	f.lastBearerJWT = bearerJWT
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

func TestHandleCreate_ForwardsBearerJWT(t *testing.T) {
	client := newFakeClient()
	h := NewHandler(client)
	body, _ := json.Marshal(createTaskRequest{Pack: "knowledge-work-pack"})
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/tasks", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	req.Header.Set("Authorization", "Bearer eyJ.fake.jwt")
	w := httptest.NewRecorder()
	h.routeTasks(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if client.lastBearerJWT != "eyJ.fake.jwt" {
		t.Errorf("expected the Authorization bearer value forwarded to Create, got %q", client.lastBearerJWT)
	}
}

// An API-key-authenticated request (Hive's own "hk_"-prefixed scheme) is not
// a Supabase JWT and edge-api's own JWT-gated /v1/artifacts route would
// never accept it, so it must never be forwarded as if it were one.
func TestHandleCreate_DoesNotForwardAPIKeyAsBearerJWT(t *testing.T) {
	client := newFakeClient()
	h := NewHandler(client)
	body, _ := json.Marshal(createTaskRequest{Pack: "coding-pack"})
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/tasks", bytes.NewReader(body))
	req = req.WithContext(userCtx(uuid.New()))
	req.Header.Set("Authorization", "Bearer hk_not_a_jwt")
	w := httptest.NewRecorder()
	h.routeTasks(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if client.lastBearerJWT != "" {
		t.Errorf("expected no bearer JWT forwarded for an API-key request, got %q", client.lastBearerJWT)
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
	gotID, suffix, ok := extractTaskPath("/v1/agent/tasks/" + id.String())
	if !ok || gotID != id || suffix != "" {
		t.Errorf("extractTaskPath failed: id=%v suffix=%q ok=%v", gotID, suffix, ok)
	}
}

func TestExtractTaskPath_Cancel(t *testing.T) {
	id := uuid.New()
	gotID, suffix, ok := extractTaskPath("/v1/agent/tasks/" + id.String() + "/cancel")
	if !ok || gotID != id || suffix != "cancel" {
		t.Errorf("extractTaskPath cancel failed: id=%v suffix=%q ok=%v", gotID, suffix, ok)
	}
}

func TestExtractTaskPath_EventsFilesSuffixes(t *testing.T) {
	id := uuid.New()
	for _, want := range []string{"events", "files"} {
		gotID, suffix, ok := extractTaskPath("/v1/agent/tasks/" + id.String() + "/" + want)
		if !ok || gotID != id || suffix != want {
			t.Errorf("%s: id=%v suffix=%q ok=%v", want, gotID, suffix, ok)
		}
	}
}

func TestExtractTaskPath_Invalid(t *testing.T) {
	for _, path := range []string{
		"/v1/agent/tasks/not-a-uuid",
		"/v1/agent/tasks/not-a-uuid/cancel",
		"/v1/agent/tasks//cancel",
		"/v1/agent/tasks/" + uuid.NewString() + "/unknown",
		"/v1/agent/tasks/" + uuid.NewString() + "/cancel/extra",
	} {
		if _, _, ok := extractTaskPath(path); ok {
			t.Errorf("path %q must not parse", path)
		}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func (f *fakeClient) Events(_ context.Context, _, _ uuid.UUID, taskID uuid.UUID, afterSeq int64, limit int) ([]Event, error) {
	if f.eventsFn != nil {
		return f.eventsFn(taskID, afterSeq, limit)
	}
	if f.tasks[taskID].ID == "" {
		return nil, ErrNotFound
	}
	if afterSeq < 0 {
		return nil, ErrCursor
	}
	return []Event{
		{Seq: 1, SourceEventID: "status:queued", Kind: "status", Payload: json.RawMessage(`{"status":"queued"}`)},
		{Seq: 2, SourceEventID: "s1", Kind: "tool_call", Payload: json.RawMessage(`{"tool_name":"bash"}`)},
	}, nil
}

func (f *fakeClient) Files(_ context.Context, _, _ uuid.UUID, taskID uuid.UUID) ([]WorkspaceFile, error) {
	if f.filesFn != nil {
		return f.filesFn(taskID)
	}
	if f.tasks[taskID].ID == "" {
		return nil, ErrNotFound
	}
	return []WorkspaceFile{{Name: "out.txt", Size: 9, ModTime: time.Now()}}, nil
}

func TestHandleEvents_HappyPath(t *testing.T) {
	h := NewHandler(newFakeClient())
	taskID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	fc := h.client.(*fakeClient)
	fc.tasks[taskID] = Task{ID: taskID.String()}

	req := httptest.NewRequest(http.MethodGet,
		"/v1/agent/tasks/11111111-1111-4111-8111-111111111111/events?after_seq=1&limit=10", nil).
		WithContext(userCtx(uuid.MustParse("22222222-2222-4222-8222-222222222222")))
	rec := httptest.NewRecorder()
	h.routeTaskByID(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Events []Event `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Events) != 2 || got.Events[0].Kind != "status" {
		t.Fatalf("events = %+v", got.Events)
	}
}

func TestHandleEvents_BadCursorIs400(t *testing.T) {
	h := NewHandler(newFakeClient())
	taskID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	h.client.(*fakeClient).tasks[taskID] = Task{ID: taskID.String()}
	tenant := uuid.MustParse("22222222-2222-4222-8222-222222222222")

	for _, q := range []string{"after_seq=-3", "after_seq=abc", "after_seq=1.5", "limit=0", "limit=-2"} {
		req := httptest.NewRequest(http.MethodGet,
			"/v1/agent/tasks/11111111-1111-4111-8111-111111111111/events?"+q, nil).
			WithContext(userCtx(tenant))
		rec := httptest.NewRecorder()
		h.routeTaskByID(rec, req)
		if rec.Code != 400 {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
}

func TestHandleEvents_LimitClampedTo500(t *testing.T) {
	tenant := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	taskID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	var gotLimit int
	fc := newFakeClient()
	fc.tasks[taskID] = Task{ID: taskID.String()}
	fc.eventsFn = func(_ uuid.UUID, _ int64, limit int) ([]Event, error) {
		gotLimit = limit
		return []Event{}, nil
	}
	req := httptest.NewRequest(http.MethodGet,
		"/v1/agent/tasks/11111111-1111-4111-8111-111111111111/events?limit=99999", nil).
		WithContext(userCtx(tenant))
	rec := httptest.NewRecorder()
	NewHandler(fc).routeTaskByID(rec, req)
	if rec.Code != 200 || gotLimit != 500 {
		t.Fatalf("status=%d limit=%d, want 200/500", rec.Code, gotLimit)
	}
}

func TestHandleEvents_Unauthenticated(t *testing.T) {
	h := NewHandler(newFakeClient())
	req := httptest.NewRequest(http.MethodGet,
		"/v1/agent/tasks/11111111-1111-4111-8111-111111111111/events", nil)
	rec := httptest.NewRecorder()
	h.routeTaskByID(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandleFiles_HappyPath(t *testing.T) {
	tenant := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	taskID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	fc := newFakeClient()
	fc.tasks[taskID] = Task{ID: taskID.String()}
	req := httptest.NewRequest(http.MethodGet,
		"/v1/agent/tasks/11111111-1111-4111-8111-111111111111/files", nil).
		WithContext(userCtx(tenant))
	rec := httptest.NewRecorder()
	NewHandler(fc).routeTaskByID(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Files []WorkspaceFile `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Files) != 1 || got.Files[0].Name != "out.txt" {
		t.Fatalf("files = %.200s (%v)", rec.Body.String(), err)
	}
}

func TestHandleEvents_FilesNotFoundIs404(t *testing.T) {
	h := NewHandler(newFakeClient())
	for _, suffix := range []string{"events", "files"} {
		req := httptest.NewRequest(http.MethodGet,
			"/v1/agent/tasks/11111111-1111-4111-8111-111111111111/"+suffix, nil).
			WithContext(userCtx(uuid.MustParse("22222222-2222-4222-8222-222222222222")))
		rec := httptest.NewRecorder()
		h.routeTaskByID(rec, req)
		if rec.Code != 404 {
			t.Errorf("%s for unknown task: status %d, want 404", suffix, rec.Code)
		}
	}
}
