package agenttask_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
)

// fakeRepository is a hand-built agenttask.Repository stub for unit tests
// that never need a live Postgres connection. Mirrors
// apps/control-plane/internal/marketplace/service_test.go's fakeRepository.
type fakeRepository struct {
	tasks map[uuid.UUID]agenttask.Task
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{tasks: make(map[uuid.UUID]agenttask.Task)}
}

func (f *fakeRepository) Create(_ context.Context, tenantID, userID uuid.UUID, pack agenttask.Pack) (agenttask.Task, error) {
	t := agenttask.Task{
		ID: uuid.New(), TenantID: tenantID, UserID: userID, Pack: pack,
		Status: agenttask.StatusQueued, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.tasks[t.ID] = t
	return t, nil
}

func (f *fakeRepository) Get(_ context.Context, tenantID, userID, id uuid.UUID) (agenttask.Task, error) {
	t, ok := f.tasks[id]
	if !ok || t.TenantID != tenantID || t.UserID != userID {
		return agenttask.Task{}, agenttask.ErrNotFound
	}
	return t, nil
}

func (f *fakeRepository) List(_ context.Context, tenantID, userID uuid.UUID) ([]agenttask.Task, error) {
	var out []agenttask.Task
	for _, t := range f.tasks {
		if t.TenantID == tenantID && t.UserID == userID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeRepository) Transition(_ context.Context, tenantID, userID, id uuid.UUID, status agenttask.Status, sessionRef, resultSummaryRef, errMsg string) (agenttask.Task, error) {
	t, ok := f.tasks[id]
	if !ok || t.TenantID != tenantID || t.UserID != userID {
		return agenttask.Task{}, agenttask.ErrNotFound
	}
	// Mirrors the real repository's atomic "not already terminal" UPDATE
	// guard: Service no longer pre-checks this itself, so the fake must
	// enforce it for terminal-rejection tests to mean anything.
	switch t.Status {
	case agenttask.StatusSucceeded, agenttask.StatusFailed, agenttask.StatusCancelled:
		return agenttask.Task{}, agenttask.ErrTerminalState
	}
	t.Status = status
	if sessionRef != "" {
		t.EngineSessionRef = sessionRef
	}
	if resultSummaryRef != "" {
		t.ResultSummaryRef = resultSummaryRef
	}
	t.ErrorMessage = errMsg
	f.tasks[id] = t
	return t, nil
}

// fakeEngine is a hand-built agenttask.Engine stub.
type fakeEngine struct {
	sessionRef string
	err        error
}

func (f *fakeEngine) Launch(context.Context, agenttask.Task) (string, error) {
	return f.sessionRef, f.err
}

func TestService_CreateTask_InvalidPack(t *testing.T) {
	svc := agenttask.NewService(newFakeRepository(), &fakeEngine{})
	_, err := svc.CreateTask(context.Background(), uuid.New(), uuid.New(), agenttask.Pack("not-a-pack"))
	if !errors.Is(err, agenttask.ErrInvalidPack) {
		t.Fatalf("expected ErrInvalidPack, got %v", err)
	}
}

func TestService_CreateTask_EngineNotConfigured_StaysQueued(t *testing.T) {
	svc := agenttask.NewService(newFakeRepository(), agenttask.NotConfiguredEngine{})
	task, err := svc.CreateTask(context.Background(), uuid.New(), uuid.New(), agenttask.PackCoding)
	if err != nil {
		t.Fatalf("CreateTask() unexpected err: %v", err)
	}
	if task.Status != agenttask.StatusQueued {
		t.Errorf("expected StatusQueued when engine not configured, got %v", task.Status)
	}
}

func TestService_CreateTask_NilEngineDefaultsToNotConfigured(t *testing.T) {
	svc := agenttask.NewService(newFakeRepository(), nil)
	task, err := svc.CreateTask(context.Background(), uuid.New(), uuid.New(), agenttask.PackKnowledgeWork)
	if err != nil {
		t.Fatalf("CreateTask() unexpected err: %v", err)
	}
	if task.Status != agenttask.StatusQueued {
		t.Errorf("expected StatusQueued with nil engine, got %v", task.Status)
	}
}

func TestService_CreateTask_EngineLaunchSucceeds_TransitionsToRunning(t *testing.T) {
	svc := agenttask.NewService(newFakeRepository(), &fakeEngine{sessionRef: "session-123"})
	task, err := svc.CreateTask(context.Background(), uuid.New(), uuid.New(), agenttask.PackCoding)
	if err != nil {
		t.Fatalf("CreateTask() unexpected err: %v", err)
	}
	if task.Status != agenttask.StatusRunning {
		t.Errorf("expected StatusRunning, got %v", task.Status)
	}
	if task.EngineSessionRef != "session-123" {
		t.Errorf("expected engine session ref to be persisted, got %q", task.EngineSessionRef)
	}
}

func TestService_CreateTask_EngineLaunchFails_TransitionsToFailed(t *testing.T) {
	svc := agenttask.NewService(newFakeRepository(), &fakeEngine{err: errors.New("sandbox unavailable")})
	task, err := svc.CreateTask(context.Background(), uuid.New(), uuid.New(), agenttask.PackCoding)
	if err != nil {
		t.Fatalf("CreateTask() unexpected err: %v", err)
	}
	if task.Status != agenttask.StatusFailed {
		t.Errorf("expected StatusFailed, got %v", task.Status)
	}
	if task.ErrorMessage == "" {
		t.Error("expected error_message to be recorded")
	}
}

func TestService_Get_WrongUserReturnsNotFound(t *testing.T) {
	repo := newFakeRepository()
	svc := agenttask.NewService(repo, &fakeEngine{})
	tenantID, ownerID, otherID := uuid.New(), uuid.New(), uuid.New()

	created, err := svc.CreateTask(context.Background(), tenantID, ownerID, agenttask.PackCoding)
	if err != nil {
		t.Fatalf("seed CreateTask: %v", err)
	}

	if _, err := svc.Get(context.Background(), tenantID, otherID, created.ID); !errors.Is(err, agenttask.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for a different user, got %v", err)
	}
	// The owner can still resume it — this is the cross-session portability path.
	if _, err := svc.Get(context.Background(), tenantID, ownerID, created.ID); err != nil {
		t.Fatalf("owner Get() unexpected err: %v", err)
	}
}

func TestService_List_ScopedToTenantAndUser(t *testing.T) {
	repo := newFakeRepository()
	svc := agenttask.NewService(repo, &fakeEngine{})
	tenantID, userA, userB := uuid.New(), uuid.New(), uuid.New()

	if _, err := svc.CreateTask(context.Background(), tenantID, userA, agenttask.PackCoding); err != nil {
		t.Fatalf("seed userA task: %v", err)
	}
	if _, err := svc.CreateTask(context.Background(), tenantID, userB, agenttask.PackCoding); err != nil {
		t.Fatalf("seed userB task: %v", err)
	}

	tasksA, err := svc.List(context.Background(), tenantID, userA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasksA) != 1 {
		t.Fatalf("expected exactly 1 task for userA, got %d", len(tasksA))
	}
}

func TestService_Cancel_FromQueued(t *testing.T) {
	svc := agenttask.NewService(newFakeRepository(), agenttask.NotConfiguredEngine{})
	tenantID, userID := uuid.New(), uuid.New()
	created, err := svc.CreateTask(context.Background(), tenantID, userID, agenttask.PackCoding)
	if err != nil {
		t.Fatalf("seed CreateTask: %v", err)
	}

	cancelled, err := svc.Cancel(context.Background(), tenantID, userID, created.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.Status != agenttask.StatusCancelled {
		t.Errorf("expected StatusCancelled, got %v", cancelled.Status)
	}
}

func TestService_Cancel_TerminalStateRejected(t *testing.T) {
	svc := agenttask.NewService(newFakeRepository(), &fakeEngine{sessionRef: "s1"})
	tenantID, userID := uuid.New(), uuid.New()
	created, err := svc.CreateTask(context.Background(), tenantID, userID, agenttask.PackCoding)
	if err != nil {
		t.Fatalf("seed CreateTask: %v", err)
	}
	if _, err := svc.Cancel(context.Background(), tenantID, userID, created.ID); err != nil {
		t.Fatalf("first Cancel: %v", err)
	}

	if _, err := svc.Cancel(context.Background(), tenantID, userID, created.ID); !errors.Is(err, agenttask.ErrTerminalState) {
		t.Fatalf("expected ErrTerminalState on a second cancel, got %v", err)
	}
}

func TestService_Cancel_UnknownTaskReturnsNotFound(t *testing.T) {
	svc := agenttask.NewService(newFakeRepository(), &fakeEngine{})
	if _, err := svc.Cancel(context.Background(), uuid.New(), uuid.New(), uuid.New()); !errors.Is(err, agenttask.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
