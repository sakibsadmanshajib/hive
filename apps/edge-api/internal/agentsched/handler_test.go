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
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/sessionbilling/billingtest"
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

// billedHandler is the handler every test builds: creating a routine is gated
// on solvency, so a handler without its accounting seam refuses.
func billedHandler(t *testing.T, client ScheduleClient) *Handler {
	t.Helper()
	acct := &billingtest.Accounting{}
	return NewHandler(client).WithBilling(acct.Client(t), billingtest.Billable())
}

func createScheduleReq(t *testing.T, tenantID uuid.UUID) *http.Request {
	t.Helper()
	body, err := json.Marshal(createReq{
		Name: "Morning digest", Instructions: "Summarize inbox", Schedule: "daily",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/schedules", bytes.NewReader(body))
	return req.WithContext(userCtx(tenantID))
}

// A routine is a task submission on a timer, so a tenant who cannot pay is
// refused here too. Gating only POST /v1/agent/tasks would leave this as the
// documented way around that gate.
func TestScheduleCreate_RefusesATenantThatCannotPay(t *testing.T) {
	acct := &billingtest.Accounting{ReservationStatus: http.StatusConflict}
	client := newFakeClient()
	h := NewHandler(client).WithBilling(acct.Client(t), billingtest.Billable())

	w := httptest.NewRecorder()
	h.routeCollection(w, createScheduleReq(t, uuid.New()))

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "insufficient_quota") {
		t.Errorf("body = %s, want insufficient_quota", w.Body.String())
	}
	if len(client.schedules) != 0 {
		t.Error("a refused creation reached control-plane anyway")
	}
}

// A billing position that cannot be read is not one that is known to be
// absent, so this refuses rather than creating.
func TestScheduleCreate_RefusesWhenTheBillingLookupFails(t *testing.T) {
	acct := &billingtest.Accounting{}
	client := newFakeClient()
	h := NewHandler(client).WithBilling(acct.Client(t), billingtest.Unreadable())

	w := httptest.NewRecorder()
	h.routeCollection(w, createScheduleReq(t, uuid.New()))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
	if len(client.schedules) != 0 {
		t.Error("a routine was created against an unknown billing position")
	}
}

// A handler built without its accounting seam refuses rather than creating a
// routine it cannot check the balance for.
func TestScheduleCreate_RefusesWhenBillingIsNotWired(t *testing.T) {
	client := newFakeClient()
	h := NewHandler(client)

	w := httptest.NewRecorder()
	h.routeCollection(w, createScheduleReq(t, uuid.New()))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
	if len(client.schedules) != 0 {
		t.Error("an ungated creation wrote a routine")
	}
}

// The launch hold is handed straight back in the same call, for the same
// reason it is on POST /v1/agent/tasks (#600).
func TestScheduleCreate_SolvencyHoldIsReleasedImmediately(t *testing.T) {
	acct := &billingtest.Accounting{}
	h := NewHandler(newFakeClient()).WithBilling(acct.Client(t), billingtest.Billable())

	w := httptest.NewRecorder()
	h.routeCollection(w, createScheduleReq(t, uuid.New()))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", w.Code, w.Body.String())
	}
	reservations, released := acct.Counts()
	if reservations != 1 || released != 1 {
		t.Fatalf("want one hold taken and handed back, got taken=%d released=%d", reservations, released)
	}
	if got := acct.Released()[0].Reason; got != "solvency_probe" {
		t.Errorf("release reason = %q, want %q", got, "solvency_probe")
	}
}

func TestScheduleRoutes_CRUDHappyPath(t *testing.T) {
	client := newFakeClient()
	h := billedHandler(t, client)
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
	h := billedHandler(t, client)
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
	h := billedHandler(t, client)
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
