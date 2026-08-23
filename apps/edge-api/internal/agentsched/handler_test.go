package agentsched

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

type fakeClient struct {
	schedules map[uuid.UUID]Schedule
	deleteErr error
}

func newFakeClient() *fakeClient {
	return &fakeClient{schedules: make(map[uuid.UUID]Schedule)}
}

func (f *fakeClient) Create(_ context.Context, tenantID, userID uuid.UUID, name, instructions, schedule string) (Schedule, error) {
	id := uuid.New()
	s := Schedule{ID: id.String(), Name: name, Instructions: instructions, Schedule: schedule, Enabled: true}
	f.schedules[id] = s
	return s, nil
}

func (f *fakeClient) List(context.Context, uuid.UUID, uuid.UUID) ([]Schedule, error) {
	out := make([]Schedule, 0, len(f.schedules))
	for _, s := range f.schedules {
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeClient) Get(_ context.Context, _, _, id uuid.UUID) (Schedule, error) {
	s, ok := f.schedules[id]
	if !ok {
		return Schedule{}, ErrNotFound
	}
	return s, nil
}

func (f *fakeClient) Update(_ context.Context, _, _, id uuid.UUID, b updateBody) (Schedule, error) {
	s, ok := f.schedules[id]
	if !ok {
		return Schedule{}, ErrNotFound
	}
	s.Name = b.Name
	s.Instructions = b.Instructions
	s.Schedule = b.Schedule
	s.Enabled = b.Enabled
	f.schedules[id] = s
	return s, nil
}

func (f *fakeClient) Delete(_ context.Context, _, _, id uuid.UUID) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.schedules, id)
	return nil
}

func userCtx(tenantID uuid.UUID) context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: uuid.New(), TenantID: tenantID})
}

func TestScheduleRoutes_CRUDHappyPath(t *testing.T) {
	client := newFakeClient()
	h := NewHandler(client)
	mux := http.NewServeMux()
	h.Register(mux)
	tenantID := uuid.New()

	// Create
	body, _ := json.Marshal(createReq{
		Name: "Morning digest", Instructions: "Summarize inbox", Schedule: "daily",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/schedules", bytes.NewReader(body))
	req = req.WithContext(userCtx(tenantID))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var created Schedule
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if _, err := uuid.Parse(created.ID); err != nil {
		t.Fatalf("created id not a uuid: %v", err)
	}

	// List
	req = httptest.NewRequest(http.MethodGet, "/v1/agent/schedules", nil)
	req = req.WithContext(userCtx(tenantID))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Morning digest") {
		t.Fatal("list must include the created schedule")
	}
}

func TestScheduleRoutes_UpdateToggleAndDelete(t *testing.T) {
	client := newFakeClient()
	h := NewHandler(client)
	mux := http.NewServeMux()
	h.Register(mux)
	tenantID := uuid.New()

	body, _ := json.Marshal(createReq{Name: "n", Instructions: "i", Schedule: "daily"})
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/schedules", bytes.NewReader(body))
	req = req.WithContext(userCtx(tenantID))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", w.Code)
	}
	var created Schedule
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	id := created.ID

	// Update (full PUT body)
	ub, _ := json.Marshal(updateBody{
		Name: "n2", Instructions: "i2", Schedule: "weekly", Enabled: false,
	})
	req = httptest.NewRequest(http.MethodPut, "/v1/agent/schedules/"+id, bytes.NewReader(ub))
	req = req.WithContext(userCtx(tenantID))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Get shows the toggle
	req = httptest.NewRequest(http.MethodGet, "/v1/agent/schedules/"+id, nil)
	req = req.WithContext(userCtx(tenantID))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var got Schedule
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Enabled {
		t.Fatal("expected disabled after PUT with enabled=false")
	}

	// Delete then 404
	req = httptest.NewRequest(http.MethodDelete, "/v1/agent/schedules/"+id, nil)
	req = req.WithContext(userCtx(tenantID))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d", w.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/agent/schedules/"+id, nil)
	req = req.WithContext(userCtx(tenantID))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete: expected 404, got %d", w.Code)
	}
}

func TestScheduleRoutes_ValidationErrorsAre400Not500(t *testing.T) {
	client := newFakeClient()
	client.deleteErr = errors.New("boom")
	h := NewHandler(client)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodDelete, "/v1/agent/schedules/"+uuid.NewString(), nil)
	req = req.WithContext(userCtx(uuid.New()))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for a client transport error, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/agent/schedules", bytes.NewReader([]byte("{bad json")))
	req = req.WithContext(userCtx(uuid.New()))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed body, got %d", w.Code)
	}
}
