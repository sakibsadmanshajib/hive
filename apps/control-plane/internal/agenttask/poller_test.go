package agenttask_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
)

// quietPollerLogger discards WARN logs from intentional-error test cases so
// `go test` output isn't cluttered with expected failures.
func quietPollerLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeStatusChecker is a hand-built agenttask.StatusChecker stub: a fixed
// per-sessionRef response table, with an optional call counter for the
// Start/Stop loop tests.
type fakeStatusChecker struct {
	mu        sync.Mutex
	responses map[string]checkerResponse
	calls     int
}

type checkerResponse struct {
	status        agenttask.Status
	resultSummary string
	errMessage    string
	err           error
}

func (f *fakeStatusChecker) Status(_ context.Context, sessionRef string) (agenttask.Status, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	resp, ok := f.responses[sessionRef]
	if !ok {
		return agenttask.StatusRunning, "", "", nil
	}
	return resp.status, resp.resultSummary, resp.errMessage, resp.err
}

func (f *fakeStatusChecker) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newActiveTask(repo *fakeRepository, status agenttask.Status, sessionRef string) agenttask.Task {
	t, _ := repo.Create(context.Background(), uuid.New(), uuid.New(), agenttask.PackCoding, "")
	t, _ = repo.Transition(context.Background(), t.TenantID, t.UserID, t.ID, status, sessionRef, "", "")
	return t
}

func TestPoller_RunOnce_AdvancesRunningToSucceeded(t *testing.T) {
	repo := newFakeRepository()
	task := newActiveTask(repo, agenttask.StatusRunning, "session-1")
	checker := &fakeStatusChecker{responses: map[string]checkerResponse{
		"session-1": {status: agenttask.StatusSucceeded, resultSummary: "done"},
	}}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Logger: quietPollerLogger()})

	advanced, err := p.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if advanced != 1 {
		t.Fatalf("advanced=%d want 1", advanced)
	}
	got, err := repo.Get(context.Background(), task.TenantID, task.UserID, task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != agenttask.StatusSucceeded {
		t.Fatalf("status=%q want succeeded", got.Status)
	}
	if got.ResultSummaryRef != "done" {
		t.Fatalf("result summary=%q want %q", got.ResultSummaryRef, "done")
	}
}

func TestPoller_RunOnce_LeavesNonTerminalTasksAlone(t *testing.T) {
	repo := newFakeRepository()
	task := newActiveTask(repo, agenttask.StatusRunning, "session-1")
	checker := &fakeStatusChecker{responses: map[string]checkerResponse{
		"session-1": {status: agenttask.StatusRunning},
	}}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Logger: quietPollerLogger()})

	advanced, err := p.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if advanced != 0 {
		t.Fatalf("advanced=%d want 0", advanced)
	}
	got, _ := repo.Get(context.Background(), task.TenantID, task.UserID, task.ID)
	if got.Status != agenttask.StatusRunning {
		t.Fatalf("status=%q want still running", got.Status)
	}
}

func TestPoller_RunOnce_EngineErrorIsRetriedNotFailed(t *testing.T) {
	repo := newFakeRepository()
	broken := newActiveTask(repo, agenttask.StatusRunning, "session-broken")
	healthy := newActiveTask(repo, agenttask.StatusRunning, "session-healthy")
	checker := &fakeStatusChecker{responses: map[string]checkerResponse{
		"session-broken":  {err: errors.New("agent-server unreachable")},
		"session-healthy": {status: agenttask.StatusSucceeded, resultSummary: "ok"},
	}}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Logger: quietPollerLogger()})

	advanced, err := p.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected RunOnce to report the per-task engine error")
	}
	// "Retried not failed": the broken task is untouched (still active, will
	// be retried next pass), and the healthy task in the same pass still
	// advanced despite the other task's error.
	if advanced != 1 {
		t.Fatalf("advanced=%d want 1 (only the healthy task)", advanced)
	}
	gotBroken, _ := repo.Get(context.Background(), broken.TenantID, broken.UserID, broken.ID)
	if gotBroken.Status != agenttask.StatusRunning {
		t.Fatalf("broken task status=%q want unchanged (still running)", gotBroken.Status)
	}
	gotHealthy, _ := repo.Get(context.Background(), healthy.TenantID, healthy.UserID, healthy.ID)
	if gotHealthy.Status != agenttask.StatusSucceeded {
		t.Fatalf("healthy task status=%q want succeeded", gotHealthy.Status)
	}
}

// raceRepo simulates a task that ListActive still reports as active (it was
// queued/running at the moment the poller listed it) but whose Transition
// call loses a race to a concurrent Cancel landing in between — the actual
// scenario Repository.Transition's atomic "not already terminal" guard
// exists for.
type raceRepo struct {
	*fakeRepository
	active agenttask.Task
}

func (r *raceRepo) ListActive(context.Context) ([]agenttask.Task, error) {
	return []agenttask.Task{r.active}, nil
}

func (r *raceRepo) Transition(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, agenttask.Status, string, string, string) (agenttask.Task, error) {
	return agenttask.Task{}, agenttask.ErrTerminalState
}

func TestPoller_RunOnce_SwallowsTerminalStateRace(t *testing.T) {
	inner := newFakeRepository()
	task := newActiveTask(inner, agenttask.StatusRunning, "session-1")
	repo := &raceRepo{fakeRepository: inner, active: task}
	checker := &fakeStatusChecker{responses: map[string]checkerResponse{
		"session-1": {status: agenttask.StatusFailed, errMessage: "too late"},
	}}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Logger: quietPollerLogger()})

	advanced, err := p.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("expected ErrTerminalState to be swallowed, not surfaced as a pass error, got %v", err)
	}
	if advanced != 0 {
		t.Fatalf("advanced=%d want 0 (task was already terminal, not re-counted)", advanced)
	}
}

func TestPoller_RunOnce_SanitizesErrorMessageBeforePersisting(t *testing.T) {
	repo := newFakeRepository()
	task := newActiveTask(repo, agenttask.StatusRunning, "session-1")
	rawDetail := "raw provider detail: dial tcp 10.0.0.5:443: connection refused"
	checker := &fakeStatusChecker{responses: map[string]checkerResponse{
		"session-1": {status: agenttask.StatusFailed, errMessage: rawDetail},
	}}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Logger: quietPollerLogger()})

	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got, err := repo.Get(context.Background(), task.TenantID, task.UserID, task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != agenttask.StatusFailed {
		t.Fatalf("status=%q want failed", got.Status)
	}
	if strings.Contains(got.ErrorMessage, "10.0.0.5") || strings.Contains(got.ErrorMessage, "dial tcp") {
		t.Fatalf("error_message leaked raw engine/provider detail (provider-blind violation): %q", got.ErrorMessage)
	}
	if got.ErrorMessage == "" {
		t.Fatal("expected a generic non-empty error_message, got empty")
	}
}

func TestPoller_RunOnce_ListActiveErrorPropagates(t *testing.T) {
	repo := &erroringListRepo{fakeRepository: newFakeRepository()}
	checker := &fakeStatusChecker{}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Logger: quietPollerLogger()})

	if _, err := p.RunOnce(context.Background()); err == nil {
		t.Fatal("expected ListActive failure to propagate")
	}
}

type erroringListRepo struct {
	*fakeRepository
}

func (e *erroringListRepo) ListActive(context.Context) ([]agenttask.Task, error) {
	return nil, errors.New("db unavailable")
}

func TestPoller_StartStop_TicksAtInterval(t *testing.T) {
	t.Parallel()
	repo := newFakeRepository()
	newActiveTask(repo, agenttask.StatusRunning, "session-1") // gives each pass something to check
	checker := &fakeStatusChecker{}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Interval: 20 * time.Millisecond, Logger: quietPollerLogger()})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	time.Sleep(80 * time.Millisecond)
	p.Stop()

	// Eager first pass + at least 2 tick-driven passes within 80ms at a 20ms
	// interval: >=1 would also pass on the eager pass alone and never prove
	// the ticker itself fires.
	if calls := checker.Calls(); calls < 2 {
		t.Fatalf("expected >=2 calls within window (eager pass + ticks), got %d", calls)
	}
}

func TestPoller_Start_DoubleCallIsNoop(t *testing.T) {
	t.Parallel()
	repo := newFakeRepository()
	checker := &fakeStatusChecker{}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Interval: 50 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	p.Start(ctx) // ignored
	time.Sleep(10 * time.Millisecond)
	p.Stop()
	p.Stop() // double-stop also safe
}

// blockingStatusChecker blocks each Status call on release, and records
// whether more than one call was ever in flight at once — the signal a
// concurrent Start racing an in-progress Stop would produce (a second loop
// launched while the first pass is still running).
type blockingStatusChecker struct {
	mu          sync.Mutex
	inFlight    int
	maxInFlight int
	release     <-chan struct{}
	entered     chan<- struct{}
}

func (b *blockingStatusChecker) Status(context.Context, string) (agenttask.Status, string, string, error) {
	b.mu.Lock()
	b.inFlight++
	if b.inFlight > b.maxInFlight {
		b.maxInFlight = b.inFlight
	}
	b.mu.Unlock()

	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release

	b.mu.Lock()
	b.inFlight--
	b.mu.Unlock()
	return agenttask.StatusRunning, "", "", nil
}

func (b *blockingStatusChecker) maxConcurrent() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.maxInFlight
}

func TestPoller_Stop_BlocksConcurrentStartUntilLoopExits(t *testing.T) {
	repo := newFakeRepository()
	newActiveTask(repo, agenttask.StatusRunning, "session-1")

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	checker := &blockingStatusChecker{release: release, entered: entered}
	// Long interval: only the eager first pass should ever run in this test.
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Interval: time.Hour, Logger: quietPollerLogger()})

	p.Start(context.Background())
	<-entered // eager first pass is now blocked inside Status

	stopDone := make(chan struct{})
	go func() {
		p.Stop()
		close(stopDone)
	}()

	// Give Stop time to reach its <-doneCh wait, then try Start again: with
	// started cleared only after that wait, this must stay a no-op while
	// the original pass is still blocked.
	time.Sleep(20 * time.Millisecond)
	p.Start(context.Background())

	close(release) // let the blocked pass finish
	<-stopDone

	if max := checker.maxConcurrent(); max > 1 {
		t.Fatalf("expected at most 1 concurrent Status call, got %d (a concurrent Start raced Stop-in-progress and launched a second loop)", max)
	}
}

func TestNewPoller_PanicsOnNilDependencies(t *testing.T) {
	repo := newFakeRepository()
	checker := &fakeStatusChecker{}

	assertPanics(t, func() { agenttask.NewPoller(nil, checker, agenttask.PollerConfig{Logger: quietPollerLogger()}) })
	assertPanics(t, func() { agenttask.NewPoller(repo, nil, agenttask.PollerConfig{Logger: quietPollerLogger()}) })
}

func assertPanics(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	fn()
}
