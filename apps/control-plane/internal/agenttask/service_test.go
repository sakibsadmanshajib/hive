package agenttask_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
)

// fakeRepository is a hand-built agenttask.Repository stub for unit tests
// that never need a live Postgres connection. Mirrors
// apps/control-plane/internal/marketplace/service_test.go's fakeRepository.
// Guarded by a mutex because CreateTask now launches on a background
// goroutine (issue #881), so a launch's Transition can land while the test
// goroutine is reading.
type fakeRepository struct {
	mu    sync.Mutex
	tasks map[uuid.UUID]agenttask.Task

	// failTransitionTo makes Transition return failTransitionErr for that
	// target status, standing in for a real database failure (pool exhausted,
	// statement timeout, connectivity blip) rather than a state-machine
	// rejection. Without it no test could reach the branches that only a
	// non-ErrTerminalState error takes.
	failTransitionTo  agenttask.Status
	failTransitionErr error
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{tasks: make(map[uuid.UUID]agenttask.Task)}
}

func (f *fakeRepository) Create(_ context.Context, tenantID, userID uuid.UUID, pack agenttask.Pack, instructions string) (agenttask.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := agenttask.Task{
		ID: uuid.New(), TenantID: tenantID, UserID: userID, Pack: pack, Instructions: instructions,
		Status: agenttask.StatusQueued, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.tasks[t.ID] = t
	return t, nil
}

func (f *fakeRepository) Get(_ context.Context, tenantID, userID, id uuid.UUID) (agenttask.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok || t.TenantID != tenantID || t.UserID != userID {
		return agenttask.Task{}, agenttask.ErrNotFound
	}
	return t, nil
}

func (f *fakeRepository) List(_ context.Context, tenantID, userID uuid.UUID) ([]agenttask.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []agenttask.Task
	for _, t := range f.tasks {
		if t.TenantID == tenantID && t.UserID == userID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeRepository) Transition(_ context.Context, tenantID, userID, id uuid.UUID, status agenttask.Status, sessionRef, resultSummaryRef, errMsg string) (agenttask.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failTransitionErr != nil && status == f.failTransitionTo {
		return agenttask.Task{}, f.failTransitionErr
	}
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

func (f *fakeRepository) ListActive(context.Context) ([]agenttask.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []agenttask.Task
	for _, t := range f.tasks {
		if (t.Status == agenttask.StatusQueued || t.Status == agenttask.StatusRunning) && t.EngineSessionRef != "" {
			out = append(out, t)
		}
	}
	return out, nil
}

// fakeEngine is a hand-built agenttask.Engine stub.
type fakeEngine struct {
	sessionRef string
	err        error

	mu        sync.Mutex
	cancelled []string
}

func (f *fakeEngine) Launch(context.Context, agenttask.Task) (string, error) {
	return f.sessionRef, f.err
}

func (f *fakeEngine) Cancel(_ context.Context, sessionRef string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, sessionRef)
	return nil
}

func (f *fakeEngine) cancelCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cancelled)
}

// errNoSlot stands in for apps/agent-engine/internal/quota's
// ErrUserQuotaExceeded, which control-plane cannot import across the module
// boundary (that package is under apps/agent-engine/internal).
var errNoSlot = errors.New("test engine: user concurrency limit reached")

// slotEngine models the launcher's per-user concurrency accounting
// (apps/agent-engine/internal/quota.Manager): Launch takes a slot and refuses
// once the ceiling is reached, and only ending the session (Cancel here, or a
// terminal Status on the real launcher) gives one back.
//
// It is deliberately the smallest stand-in that can tell "cancel reached the
// engine" apart from "cancel wrote a database row and nothing else", which is
// the whole of issue #886: a test that asserted only the returned status would
// have passed against the broken code.
type slotEngine struct {
	mu     sync.Mutex
	limit  int
	live   map[string]bool
	seq    int
	cancel int

	// launchGate, when non-nil, blocks each Launch after it has taken its
	// slot and before it returns a session reference — the window in which a
	// cancel can arrive before the launch finishes.
	launchGate chan struct{}
}

func newSlotEngine(limit int) *slotEngine {
	return &slotEngine{limit: limit, live: make(map[string]bool)}
}

func (s *slotEngine) Launch(context.Context, agenttask.Task) (string, error) {
	s.mu.Lock()
	if len(s.live) >= s.limit {
		s.mu.Unlock()
		return "", errNoSlot
	}
	s.seq++
	ref := fmt.Sprintf("session-%d", s.seq)
	s.live[ref] = true
	gate := s.launchGate
	s.mu.Unlock()

	if gate != nil {
		<-gate
	}
	return ref, nil
}

func (s *slotEngine) Cancel(_ context.Context, sessionRef string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel++
	if !s.live[sessionRef] {
		return fmt.Errorf("test engine: unknown session %q", sessionRef)
	}
	delete(s.live, sessionRef)
	return nil
}

// slotsInUse is the assertion that matters: it is the counter the live demo
// box exhausted, not the task's status column.
func (s *slotEngine) slotsInUse() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.live)
}

func (s *slotEngine) cancelCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel
}

// createSettled creates a task and waits for its background launch to
// finish, returning the task as the store holds it afterwards. CreateTask
// returns as soon as the row exists (issue #881), so every assertion about a
// launch outcome has to read the settled row rather than the create return.
func createSettled(t *testing.T, svc *agenttask.Service, tenantID, userID uuid.UUID, pack agenttask.Pack) agenttask.Task {
	t.Helper()
	created, err := svc.CreateTask(context.Background(), tenantID, userID, pack, "")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	svc.WaitIdle()
	settled, err := svc.Get(context.Background(), tenantID, userID, created.ID)
	if err != nil {
		t.Fatalf("Get after launch: %v", err)
	}
	return settled
}

// createWithoutWaiting creates a task against an engine whose launch is
// gated open, and fails the test if the create call does not return promptly.
// It runs the call on its own goroutine so a create that blocks on the launch
// (issue #881) fails this one test with a legible message instead of hanging
// the whole package until the go test timeout.
func createWithoutWaiting(t *testing.T, svc *agenttask.Service, tenantID, userID uuid.UUID) agenttask.Task {
	t.Helper()
	type result struct {
		task agenttask.Task
		err  error
	}
	done := make(chan result, 1)
	go func() {
		task, err := svc.CreateTask(context.Background(), tenantID, userID, agenttask.PackCoding, "")
		done <- result{task: task, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("CreateTask: %v", got.err)
		}
		return got.task
	case <-time.After(2 * time.Second):
		t.Fatal("CreateTask blocked on the engine launch (issue #881): edge-api gives up at 15s and reports a failure for a task that is in fact starting")
		return agenttask.Task{}
	}
}

func TestService_CreateTask_InvalidPack(t *testing.T) {
	svc := agenttask.NewService(newFakeRepository(), &fakeEngine{})
	_, err := svc.CreateTask(context.Background(), uuid.New(), uuid.New(), agenttask.Pack("not-a-pack"), "")
	if !errors.Is(err, agenttask.ErrInvalidPack) {
		t.Fatalf("expected ErrInvalidPack, got %v", err)
	}
}

// TestService_CreateTask_EngineNotConfigured_FailsVisibly guards the bug
// this fix closes: a task submitted while the agent engine is unconfigured
// must never come back looking like a healthy queued task that will
// eventually run. It has to land in StatusFailed, with a non-empty
// error_message, so a caller can tell "queued and progressing" apart from
// "will never run" — see .wolf/buglog for the original report ("stuck
// forever in queue, no error surfaced").
func TestService_CreateTask_EngineNotConfigured_FailsVisibly(t *testing.T) {
	svc := agenttask.NewService(newFakeRepository(), agenttask.NotConfiguredEngine{})
	task := createSettled(t, svc, uuid.New(), uuid.New(), agenttask.PackCoding)
	if task.Status != agenttask.StatusFailed {
		t.Errorf("expected StatusFailed when engine not configured (must not stay queued forever), got %v", task.Status)
	}
	if task.ErrorMessage == "" {
		t.Error("expected a non-empty error_message explaining the task will never run")
	}
	if strings.Contains(task.ErrorMessage, "HIVE_AGENT_ENGINE") {
		t.Errorf("error_message must stay customer-safe, not leak env var / deployment detail: %q", task.ErrorMessage)
	}
}

func TestService_CreateTask_NilEngineDefaultsToNotConfigured(t *testing.T) {
	svc := agenttask.NewService(newFakeRepository(), nil)
	task := createSettled(t, svc, uuid.New(), uuid.New(), agenttask.PackKnowledgeWork)
	if task.Status != agenttask.StatusFailed {
		t.Errorf("expected StatusFailed with nil engine (defaults to NotConfiguredEngine), got %v", task.Status)
	}
}

func TestService_CreateTask_EngineLaunchSucceeds_TransitionsToRunning(t *testing.T) {
	svc := agenttask.NewService(newFakeRepository(), &fakeEngine{sessionRef: "session-123"})
	task := createSettled(t, svc, uuid.New(), uuid.New(), agenttask.PackCoding)
	if task.Status != agenttask.StatusRunning {
		t.Errorf("expected StatusRunning, got %v", task.Status)
	}
	if task.EngineSessionRef != "session-123" {
		t.Errorf("expected engine session ref to be persisted, got %q", task.EngineSessionRef)
	}
}

// TestService_CreateTask_EngineLaunchFails_SanitizesErrorMessage guards the
// PR #606 review finding: a generic Launch failure (anything but
// ErrEngineNotConfigured) must never persist err.Error() verbatim into the
// customer-visible error_message. That field is returned by the HTTP handler
// (see http.go's taskResponse), so an arbitrary engine error carrying a
// provider name, internal hostname, or upstream error body must never reach
// it. The raw detail still needs to reach an operator (WarnContext log in
// service.go's default case), just not this field.
func TestService_CreateTask_EngineLaunchFails_SanitizesErrorMessage(t *testing.T) {
	rawErr := "dial tcp acme-inference-provider.internal:443: connection refused"
	svc := agenttask.NewService(newFakeRepository(), &fakeEngine{err: errors.New(rawErr)})
	task := createSettled(t, svc, uuid.New(), uuid.New(), agenttask.PackCoding)
	if task.Status != agenttask.StatusFailed {
		t.Errorf("expected StatusFailed, got %v", task.Status)
	}
	if task.ErrorMessage == "" {
		t.Fatal("expected error_message to be recorded")
	}
	if task.ErrorMessage == rawErr || strings.Contains(task.ErrorMessage, "acme-inference-provider") {
		t.Errorf("error_message must stay provider-blind, not persist the raw engine error verbatim: %q", task.ErrorMessage)
	}
	const wantSanitized = "agent engine could not start the task"
	if task.ErrorMessage != wantSanitized {
		t.Errorf("expected sanitized error_message %q, got %q", wantSanitized, task.ErrorMessage)
	}
}

func TestService_Get_WrongUserReturnsNotFound(t *testing.T) {
	repo := newFakeRepository()
	svc := agenttask.NewService(repo, &fakeEngine{})
	tenantID, ownerID, otherID := uuid.New(), uuid.New(), uuid.New()

	created, err := svc.CreateTask(context.Background(), tenantID, ownerID, agenttask.PackCoding, "")
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

	if _, err := svc.CreateTask(context.Background(), tenantID, userA, agenttask.PackCoding, ""); err != nil {
		t.Fatalf("seed userA task: %v", err)
	}
	if _, err := svc.CreateTask(context.Background(), tenantID, userB, agenttask.PackCoding, ""); err != nil {
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

func TestService_Cancel_FromRunning(t *testing.T) {
	// A NotConfiguredEngine seed would now land the task in StatusFailed
	// (terminal) rather than StatusQueued, so this uses a launching
	// fakeEngine to reach the other cancellable state, StatusRunning.
	eng := &fakeEngine{sessionRef: "session-cancel"}
	svc := agenttask.NewService(newFakeRepository(), eng)
	tenantID, userID := uuid.New(), uuid.New()
	created := createSettled(t, svc, tenantID, userID, agenttask.PackCoding)

	cancelled, err := svc.Cancel(context.Background(), tenantID, userID, created.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.Status != agenttask.StatusCancelled {
		t.Errorf("expected StatusCancelled, got %v", cancelled.Status)
	}
	svc.WaitIdle() // the engine stop runs in the background, same as the launch
	if got := eng.cancelCount(); got != 1 {
		t.Errorf("expected the engine session to be cancelled exactly once, got %d calls", got)
	}
}

// A second cancel must be rejected by the row's own terminal guard and must
// never reach the engine again: the first cancel already reaped that session,
// so a second engine call could only ever hit an unknown session (or, worse
// on a future launcher that recycles references, somebody else's).
func TestService_Cancel_TerminalStateRejected(t *testing.T) {
	eng := &fakeEngine{sessionRef: "s1"}
	svc := agenttask.NewService(newFakeRepository(), eng)
	tenantID, userID := uuid.New(), uuid.New()
	created := createSettled(t, svc, tenantID, userID, agenttask.PackCoding)
	if _, err := svc.Cancel(context.Background(), tenantID, userID, created.ID); err != nil {
		t.Fatalf("first Cancel: %v", err)
	}

	if _, err := svc.Cancel(context.Background(), tenantID, userID, created.ID); !errors.Is(err, agenttask.ErrTerminalState) {
		t.Fatalf("expected ErrTerminalState on a second cancel, got %v", err)
	}
	svc.WaitIdle()
	if got := eng.cancelCount(); got != 1 {
		t.Errorf("expected exactly one engine cancel across a double cancel, got %d", got)
	}
}

// =============================================================================
// Issue #886 — cancelling a task must release the launcher concurrency slot
// =============================================================================

// TestService_Cancel_ReleasesEngineConcurrencySlot reproduces the live
// symptom measured on the demo box on 2026-08-11: two cancelled tasks held
// both of HIVE_QUOTA_USER_CONCURRENCY's slots for over half an hour, and
// every create after that was refused instantly.
//
// The assertion is the slot counter, not the task status. Service.Cancel
// already returned a cancelled task before this fix; what it never did was
// tell the engine, so the sandbox kept running and kept its slot.
func TestService_Cancel_ReleasesEngineConcurrencySlot(t *testing.T) {
	eng := newSlotEngine(1)
	svc := agenttask.NewService(newFakeRepository(), eng)
	tenantID, userID := uuid.New(), uuid.New()

	first := createSettled(t, svc, tenantID, userID, agenttask.PackCoding)
	if first.Status != agenttask.StatusRunning {
		t.Fatalf("seed task: expected running, got %v", first.Status)
	}

	// With the only slot held, a second create fails. This is the "Blocked"
	// the console showed, and it is correct behaviour while the first task
	// really is running.
	blocked := createSettled(t, svc, tenantID, userID, agenttask.PackCoding)
	if blocked.Status != agenttask.StatusFailed {
		t.Fatalf("expected the second create to fail while the slot is held, got %v", blocked.Status)
	}

	if _, err := svc.Cancel(context.Background(), tenantID, userID, first.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	svc.WaitIdle()
	if got := eng.slotsInUse(); got != 0 {
		t.Fatalf("cancel did not release the launcher slot: %d still in use (issue #886)", got)
	}

	// The point of releasing it: the same user can work again straight away.
	next := createSettled(t, svc, tenantID, userID, agenttask.PackCoding)
	if next.Status != agenttask.StatusRunning {
		t.Fatalf("expected a create after cancel to run, got %v (%q)", next.Status, next.ErrorMessage)
	}
}

// A cancel that lands while the launch is still in flight is the nastiest
// ordering: the row has no engine_session_ref yet, so Cancel has nothing to
// stop, and the launch then completes into a task that is already terminal.
// Whoever learns the session reference last owns the teardown, which is the
// background launch.
func TestService_CancelDuringLaunch_ReleasesTheSlotThatLaunchTook(t *testing.T) {
	eng := newSlotEngine(1)
	eng.launchGate = make(chan struct{})
	svc := agenttask.NewService(newFakeRepository(), eng)
	tenantID, userID := uuid.New(), uuid.New()

	created := createWithoutWaiting(t, svc, tenantID, userID)
	if created.Status != agenttask.StatusQueued {
		t.Fatalf("expected a queued task while the launch is in flight, got %v", created.Status)
	}

	cancelled, err := svc.Cancel(context.Background(), tenantID, userID, created.ID)
	if err != nil {
		t.Fatalf("Cancel during launch: %v", err)
	}
	if cancelled.Status != agenttask.StatusCancelled {
		t.Fatalf("expected StatusCancelled, got %v", cancelled.Status)
	}

	close(eng.launchGate) // the launch now finishes into a cancelled task
	svc.WaitIdle()

	if got := eng.slotsInUse(); got != 0 {
		t.Fatalf("a launch that finished into a cancelled task kept its slot: %d in use", got)
	}
	if got := eng.cancelCount(); got != 1 {
		t.Fatalf("expected exactly one engine cancel for a cancel-during-launch, got %d", got)
	}
}

// Cancel racing a completion the poller already recorded: the row's atomic
// terminal guard rejects the cancel, and the engine must not be asked to stop
// a session the launcher has already reaped on its own.
func TestService_Cancel_LosesRaceWithCompletion_LeavesEngineAlone(t *testing.T) {
	repo := newFakeRepository()
	eng := &fakeEngine{sessionRef: "session-race"}
	svc := agenttask.NewService(repo, eng)
	tenantID, userID := uuid.New(), uuid.New()

	created := createSettled(t, svc, tenantID, userID, agenttask.PackCoding)
	// Stand in for the poller winning: the task is terminal before Cancel runs.
	if _, err := repo.Transition(context.Background(), tenantID, userID, created.ID, agenttask.StatusSucceeded, "", "summary", ""); err != nil {
		t.Fatalf("seed completion: %v", err)
	}

	if _, err := svc.Cancel(context.Background(), tenantID, userID, created.ID); !errors.Is(err, agenttask.ErrTerminalState) {
		t.Fatalf("expected ErrTerminalState, got %v", err)
	}
	svc.WaitIdle()
	if got := eng.cancelCount(); got != 0 {
		t.Errorf("expected no engine cancel for an already-terminal task, got %d", got)
	}
}

// A launch that succeeds and then cannot record itself is the nastiest
// failure in this file, and review found it uncovered: the session is stopped,
// but if the task is left in queued with an empty engine_session_ref then
// Repository.ListActive excludes it, the poller never touches it, and the
// customer watches a task that will never move and carries no error. That is
// the silent-stuck-queue shape this package already closed once, and it is
// worse than the 500 the create path used to return.
func TestService_LaunchSucceedsButTransitionFails_TaskFailsVisibly(t *testing.T) {
	repo := newFakeRepository()
	repo.failTransitionTo = agenttask.StatusRunning
	repo.failTransitionErr = errors.New("pgx: connection pool exhausted")
	eng := newSlotEngine(1)
	svc := agenttask.NewService(repo, eng)
	tenantID, userID := uuid.New(), uuid.New()

	created, err := svc.CreateTask(context.Background(), tenantID, userID, agenttask.PackCoding, "")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	svc.WaitIdle()

	settled, err := svc.Get(context.Background(), tenantID, userID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if settled.Status != agenttask.StatusFailed {
		t.Fatalf("expected StatusFailed so the task is not stranded in queued forever, got %v", settled.Status)
	}
	if settled.ErrorMessage == "" {
		t.Error("expected a customer-visible error_message rather than a silent stuck task")
	}
	if strings.Contains(settled.ErrorMessage, "pgx") || strings.Contains(settled.ErrorMessage, "pool") {
		t.Errorf("error_message must not carry the raw database failure: %q", settled.ErrorMessage)
	}
	if got := eng.slotsInUse(); got != 0 {
		t.Errorf("expected the unrecorded session to be stopped and its slot freed, %d still in use", got)
	}
}

// panickingEngine models a bug in an Engine implementation. Before the launch
// moved to its own goroutine this ran inside the HTTP handler, where
// net/http's per-connection recover contained it; a goroutine without its own
// recover turns the same bug into a process-wide crash.
type panickingEngine struct{}

func (panickingEngine) Launch(context.Context, agenttask.Task) (string, error) {
	panic("engine implementation bug")
}

func (panickingEngine) Cancel(context.Context, string) error { return nil }

func TestService_LaunchPanic_DoesNotCrashTheProcess(t *testing.T) {
	svc := agenttask.NewService(newFakeRepository(), panickingEngine{})
	tenantID, userID := uuid.New(), uuid.New()

	created, err := svc.CreateTask(context.Background(), tenantID, userID, agenttask.PackCoding, "")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	svc.WaitIdle() // an unrecovered panic here takes the whole test binary down

	settled, err := svc.Get(context.Background(), tenantID, userID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if settled.Status != agenttask.StatusFailed {
		t.Fatalf("expected a panicking launch to leave the task failed, got %v", settled.Status)
	}
	if settled.ErrorMessage == "" {
		t.Error("expected a customer-visible error_message after a panicking launch")
	}
}

// =============================================================================
// Issue #881 — create must not block the caller on the sandbox launch
// =============================================================================

// TestService_CreateTask_DoesNotBlockOnLaunch reproduces the live symptom:
// edge-api's control-plane client gives up at 15 seconds while CreateTask
// blocked inline on a launch bounded at five minutes, so the browser was told
// 500 for a task that went on to succeed.
//
// The fix is that create returns the persisted queued task as soon as the row
// exists; the launch continues in the background and the existing poll path
// carries the task to running and beyond.
func TestService_CreateTask_DoesNotBlockOnLaunch(t *testing.T) {
	eng := newSlotEngine(1)
	eng.launchGate = make(chan struct{})
	svc := agenttask.NewService(newFakeRepository(), eng)
	tenantID, userID := uuid.New(), uuid.New()

	created := createWithoutWaiting(t, svc, tenantID, userID)
	if created.Status != agenttask.StatusQueued {
		t.Fatalf("expected the caller to get a queued task, got %v", created.Status)
	}

	close(eng.launchGate)
	svc.WaitIdle()

	settled, err := svc.Get(context.Background(), tenantID, userID, created.ID)
	if err != nil {
		t.Fatalf("Get after launch: %v", err)
	}
	if settled.Status != agenttask.StatusRunning {
		t.Fatalf("expected the background launch to advance the task to running, got %v", settled.Status)
	}
	if settled.EngineSessionRef == "" {
		t.Error("expected the background launch to persist the engine session ref")
	}
}

func TestService_Cancel_UnknownTaskReturnsNotFound(t *testing.T) {
	svc := agenttask.NewService(newFakeRepository(), &fakeEngine{})
	if _, err := svc.Cancel(context.Background(), uuid.New(), uuid.New(), uuid.New()); !errors.Is(err, agenttask.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
