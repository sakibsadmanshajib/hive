package agenttask_test

import (
	"context"
	"errors"
	"fmt"
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
// Start/Stop loop tests. Also implements Cancel (agentengine.Remote and
// agentengine.Engine both do, structurally satisfying the unexported
// budgetExceededCanceler interface chargeFailureBudget type-asserts for),
// so tests can assert on the best-effort stop a budget-exhausted task
// triggers.
type fakeStatusChecker struct {
	mu        sync.Mutex
	responses map[string]checkerResponse
	calls     int
	cancelled []string
	cancelErr error
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

func (f *fakeStatusChecker) Cancel(_ context.Context, sessionRef string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, sessionRef)
	return f.cancelErr
}

func (f *fakeStatusChecker) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeStatusChecker) Cancelled() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cancelled...)
}

func newActiveTask(repo *fakeRepository, status agenttask.Status, sessionRef string) agenttask.Task {
	t, _ := repo.Create(context.Background(), uuid.New(), uuid.New(), agenttask.PackCoding, "", uuid.Nil)
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
	// A mixed pass (one task erroring, another succeeding in the same sweep)
	// must never feed loop's backoff: RunOnce's returned error means
	// ListActive itself failed, nothing else. Before this fix RunOnce
	// reported an error for ANY per-task problem, which let one permanently
	// broken task (ListActive is cross-tenant) pin the whole poller's shared
	// interval at maxPollerBackoff for as long as that row existed,
	// throttling every other tenant's poll cadence too (measured live
	// 2026-08-16, task f9409763).
	if err != nil {
		t.Fatalf("expected a mixed pass (one error, one success) to report no error, got %v", err)
	}
	// "Retried not failed": the broken task is untouched (still active, will
	// be retried next pass, charged against its own failure budget), and the
	// healthy task in the same pass still advanced despite the other task's
	// error.
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

// TestPoller_RunOnce_AllTasksErroringNeverBacksOffEither locks in that even
// the "everything looks down" pass (no task anywhere succeeds) still does not
// feed loop's backoff: RunOnce's error is scoped to ListActive alone. A
// widespread engine outage costs each task its own maxTaskFailureBudget
// retries, never the shared poll cadence.
func TestPoller_RunOnce_AllTasksErroringNeverBacksOffEither(t *testing.T) {
	repo := newFakeRepository()
	newActiveTask(repo, agenttask.StatusRunning, "session-a")
	newActiveTask(repo, agenttask.StatusRunning, "session-b")
	checker := &fakeStatusChecker{responses: map[string]checkerResponse{
		"session-a": {err: errors.New("agent-server unreachable")},
		"session-b": {err: errors.New("agent-server unreachable")},
	}}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Logger: quietPollerLogger()})

	advanced, err := p.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("expected no error even when every active task's status check failed, got %v", err)
	}
	if advanced != 0 {
		t.Fatalf("advanced=%d want 0", advanced)
	}
}

// TestPoller_ConsecutiveFailures_OnePermanentlyFailingTaskDoesNotBackOffTheSweep
// is the loop-level regression guard for the same bug, driven across several
// simulated passes the way loop() itself does: a task whose engine session is
// permanently unreachable (any ordinary error, not ErrEngineSessionGone) sits
// in ListActive next to a perfectly healthy task that answers cleanly every
// pass without ever reaching a terminal state. consecutiveFailures must stay
// at 0 the whole time, which is exactly the input pollerBackoffDelay needs to
// keep returning the configured base interval (see TestPollerBackoffDelay);
// before this fix it grew without bound and pinned the shared poll cadence at
// maxPollerBackoff for every other tenant's tasks too. Runs fewer passes than
// maxTaskFailureBudget so the broken task's own budget never exhausts either —
// this test is purely about the shared cadence, not the per-task ceiling
// (see TestPoller_RunOnce_TaskExceedingFailureBudgetFailsRegardlessOfErrorShape
// for that).
func TestPoller_ConsecutiveFailures_OnePermanentlyFailingTaskDoesNotBackOffTheSweep(t *testing.T) {
	repo := newFakeRepository()
	newActiveTask(repo, agenttask.StatusRunning, "session-broken")
	newActiveTask(repo, agenttask.StatusRunning, "session-healthy")
	checker := &fakeStatusChecker{responses: map[string]checkerResponse{
		"session-broken":  {err: errors.New("agent-server unreachable")},
		"session-healthy": {status: agenttask.StatusRunning},
	}}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Logger: quietPollerLogger()})

	consecutiveFailures := 0
	for pass := 0; pass < 10; pass++ {
		if _, err := p.RunOnce(context.Background()); err != nil {
			consecutiveFailures++
		} else {
			consecutiveFailures = 0
		}
	}
	if consecutiveFailures != 0 {
		t.Fatalf("consecutiveFailures=%d after 10 passes with one permanently broken task alongside a healthy one, want 0 (base interval, no backoff)", consecutiveFailures)
	}
}

// TestPoller_RunOnce_SingleTaskErroringForeverDoesNotBackOffTheSweep is the
// direct regression guard for the reviewed HIGH: the demo box's own quota (2
// per user, 4 per tenant) makes a SINGLE active task the ordinary shape, not
// a corner case, and an earlier version of this fix (errCount == len(tasks))
// collapsed back to "any single task's own error" exactly in that shape,
// since a fraction of 1 out of 1 is trivially 100%. With only one active task
// in the whole system erroring every single pass (never reaching
// maxTaskFailureBudget within this loop), RunOnce must still report no error
// on every pass.
func TestPoller_RunOnce_SingleTaskErroringForeverDoesNotBackOffTheSweep(t *testing.T) {
	repo := newFakeRepository()
	newActiveTask(repo, agenttask.StatusRunning, "session-only")
	checker := &fakeStatusChecker{responses: map[string]checkerResponse{
		"session-only": {err: errors.New("agent-server unreachable")},
	}}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Logger: quietPollerLogger()})

	for pass := 0; pass < 10; pass++ {
		if _, err := p.RunOnce(context.Background()); err != nil {
			t.Fatalf("pass %d: expected no error with a single perpetually-erroring active task, got %v", pass, err)
		}
	}
}

// TestPoller_RunOnce_TaskExceedingFailureBudgetFailsRegardlessOfErrorShape is
// the per-task-budget regression guard the review found missing: a task
// whose engine session was killed some way other than through Cancel or a
// normal terminal status (an OOM, an Apptainer crash) never reaches
// SandboxEngine.reap and fails its Status call forever with a plain error —
// NOT one wrapping ErrEngineSessionGone, since the launcher's serve.go only
// maps engineapi.ErrUnknownSession (a 404) onto that sentinel; this failure
// shape is whatever the daemon's default error-mapping branch produces (a
// 502 in practice). RunOnce must still eventually give up on it, exactly at
// the poller's own taskFailureBudget()'th consecutive pass (20 at the
// default 15s interval, since maxTaskFailureDuration is 5 minutes). It must
// also best-effort stop the still-live session at that point: a non-404
// error means the launcher answered from its own handler, so
// SandboxEngine.reap never ran and the session is still holding its
// concurrency slot.
func TestPoller_RunOnce_TaskExceedingFailureBudgetFailsRegardlessOfErrorShape(t *testing.T) {
	repo := newFakeRepository()
	task := newActiveTask(repo, agenttask.StatusRunning, "session-dead-sandbox")
	checker := &fakeStatusChecker{responses: map[string]checkerResponse{
		// A plain error, deliberately NOT wrapping agenttask.ErrEngineSessionGone
		// — stands in for the 502 an externally killed sandbox produces.
		"session-dead-sandbox": {err: errors.New("agentengine: /status: status 502")},
	}}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Logger: quietPollerLogger()})

	const budget = 20 // default interval 15s, maxTaskFailureDuration 5m: 5m/15s = 20
	for pass := 1; pass < budget; pass++ {
		if _, err := p.RunOnce(context.Background()); err != nil {
			t.Fatalf("pass %d: expected no error (per-task budget, not systemic), got %v", pass, err)
		}
		got, getErr := repo.Get(context.Background(), task.TenantID, task.UserID, task.ID)
		if getErr != nil {
			t.Fatalf("pass %d: Get: %v", pass, getErr)
		}
		if got.Status != agenttask.StatusRunning {
			t.Fatalf("pass %d: status=%q want still running (budget not yet exhausted)", pass, got.Status)
		}
	}
	if cancelled := checker.Cancelled(); len(cancelled) != 0 {
		t.Fatalf("expected no Cancel calls before the budget is exhausted, got %v", cancelled)
	}

	// The budget-exhausting pass.
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatalf("final pass: expected no error, got %v", err)
	}
	got, getErr := repo.Get(context.Background(), task.TenantID, task.UserID, task.ID)
	if getErr != nil {
		t.Fatalf("Get: %v", getErr)
	}
	if got.Status != agenttask.StatusFailed {
		t.Fatalf("status=%q want failed once the failure budget is exhausted", got.Status)
	}
	if got.ErrorMessage == "" || strings.Contains(got.ErrorMessage, "502") {
		t.Fatalf("error_message=%q want a non-empty, provider-blind message", got.ErrorMessage)
	}
	if cancelled := checker.Cancelled(); len(cancelled) != 1 || cancelled[0] != "session-dead-sandbox" {
		t.Fatalf("expected exactly one best-effort Cancel(\"session-dead-sandbox\") once the budget was exhausted, got %v", cancelled)
	}

	// Never rechecked again once terminal.
	callsBefore := checker.Calls()
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatalf("post-failure RunOnce: %v", err)
	}
	if got := checker.Calls(); got != callsBefore {
		t.Fatalf("expected 0 additional Status calls once the task is terminal, calls went from %d to %d", callsBefore, got)
	}
}

// TestPoller_RunOnce_FailureBudgetScalesWithConfiguredInterval is the
// regression guard for the review finding that a fixed 20-pass budget would
// silently shorten the real kill timeout whenever
// HIVE_AGENT_TASK_POLL_INTERVAL is tuned. At a 1 minute interval,
// maxTaskFailureDuration (5 minutes) must yield a 5-pass budget, not 20.
func TestPoller_RunOnce_FailureBudgetScalesWithConfiguredInterval(t *testing.T) {
	repo := newFakeRepository()
	task := newActiveTask(repo, agenttask.StatusRunning, "session-dead-sandbox")
	checker := &fakeStatusChecker{responses: map[string]checkerResponse{
		"session-dead-sandbox": {err: errors.New("agentengine: /status: status 502")},
	}}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Interval: time.Minute, Logger: quietPollerLogger()})

	const budget = 5 // maxTaskFailureDuration (5m) / 1m interval
	for pass := 1; pass < budget; pass++ {
		if _, err := p.RunOnce(context.Background()); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		got, getErr := repo.Get(context.Background(), task.TenantID, task.UserID, task.ID)
		if getErr != nil {
			t.Fatalf("pass %d: Get: %v", pass, getErr)
		}
		if got.Status != agenttask.StatusRunning {
			t.Fatalf("pass %d: status=%q want still running (budget not yet exhausted at a 1m interval)", pass, got.Status)
		}
	}
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatalf("final pass: %v", err)
	}
	got, getErr := repo.Get(context.Background(), task.TenantID, task.UserID, task.ID)
	if getErr != nil {
		t.Fatalf("Get: %v", getErr)
	}
	if got.Status != agenttask.StatusFailed {
		t.Fatalf("status=%q want failed on the 5th consecutive failure at a 1m interval, not the 15s-interval default of 20", got.Status)
	}
}

// TestPoller_RunOnce_ConcurrentCallsDoNotCorruptTaskFailures is the
// contract-regression guard the review flagged: RunOnce is exported, and
// loop's own serialization (Start/Stop's mutex) protects loop against
// itself, not this method against a second, direct caller. An
// unsynchronized concurrent map write is a process-fatal throw Go's runtime
// detects on its own, with no recover, even without -race. This is a
// contract regression today (no shipped caller does this), not a live bug;
// still cheap to close.
func TestPoller_RunOnce_ConcurrentCallsDoNotCorruptTaskFailures(t *testing.T) {
	repo := newFakeRepository()
	for i := range 5 {
		newActiveTask(repo, agenttask.StatusRunning, fmt.Sprintf("session-%d", i))
	}
	checker := &fakeStatusChecker{responses: map[string]checkerResponse{
		"session-0": {err: errors.New("boom")},
		"session-1": {err: errors.New("boom")},
	}}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Logger: quietPollerLogger()})

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = p.RunOnce(context.Background())
		}()
	}
	wg.Wait()
}

// TestPoller_RunOnce_FailureBudgetResetsOnCleanPass proves the budget counts
// CONSECUTIVE failures, not a lifetime total: a task that fails, then
// answers cleanly once, then fails again must not be any closer to its
// budget ceiling than a task that only ever failed the second run.
func TestPoller_RunOnce_FailureBudgetResetsOnCleanPass(t *testing.T) {
	repo := newFakeRepository()
	task := newActiveTask(repo, agenttask.StatusRunning, "session-flaky")
	checker := &fakeStatusChecker{responses: map[string]checkerResponse{
		"session-flaky": {err: errors.New("agent-server unreachable")},
	}}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Logger: quietPollerLogger()})

	const budget = 20 // default interval 15s, maxTaskFailureDuration 5m: 5m/15s = 20
	for pass := 0; pass < budget-1; pass++ {
		if _, err := p.RunOnce(context.Background()); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	// One clean pass resets the streak.
	checker.mu.Lock()
	checker.responses["session-flaky"] = checkerResponse{status: agenttask.StatusRunning}
	checker.mu.Unlock()
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatalf("clean pass: %v", err)
	}
	checker.mu.Lock()
	checker.responses["session-flaky"] = checkerResponse{err: errors.New("agent-server unreachable")}
	checker.mu.Unlock()

	// budget-1 more failures: still short of a fresh budget-1 threshold, so
	// the task must remain running, not failed.
	for pass := 0; pass < budget-1; pass++ {
		if _, err := p.RunOnce(context.Background()); err != nil {
			t.Fatalf("post-reset pass %d: %v", pass, err)
		}
	}
	got, getErr := repo.Get(context.Background(), task.TenantID, task.UserID, task.ID)
	if getErr != nil {
		t.Fatalf("Get: %v", getErr)
	}
	if got.Status != agenttask.StatusRunning {
		t.Fatalf("status=%q want still running: the one clean pass should have reset the failure streak", got.Status)
	}
}

// TestPoller_RunOnce_EngineSessionGoneFailsTaskInstead is the specific
// root-cause fix: a StatusChecker error wrapping agenttask.ErrEngineSessionGone
// (the launcher has no memory of this session, e.g. it restarted) is a
// definitive answer, not a transient one, and must fail the task in this
// same pass rather than being retried forever. It must also NOT attempt a
// best-effort Cancel the way an exhausted failure budget does: the engine
// already said it has no memory of the session, so there is nothing left to
// stop.
func TestPoller_RunOnce_EngineSessionGoneFailsTaskInstead(t *testing.T) {
	repo := newFakeRepository()
	task := newActiveTask(repo, agenttask.StatusRunning, "session-lost")
	checker := &fakeStatusChecker{responses: map[string]checkerResponse{
		"session-lost": {err: fmt.Errorf("agentengine: /status: status 404: %w", agenttask.ErrEngineSessionGone)},
	}}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Logger: quietPollerLogger()})

	advanced, err := p.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if advanced != 1 {
		t.Fatalf("advanced=%d want 1 (session-gone resolves the task immediately, not a retry)", advanced)
	}
	got, getErr := repo.Get(context.Background(), task.TenantID, task.UserID, task.ID)
	if getErr != nil {
		t.Fatalf("Get: %v", getErr)
	}
	if got.Status != agenttask.StatusFailed {
		t.Fatalf("status=%q want failed", got.Status)
	}
	if got.ErrorMessage == "" {
		t.Fatal("expected a non-empty customer-visible error_message")
	}
	if strings.Contains(got.ErrorMessage, "404") || strings.Contains(got.ErrorMessage, "session-lost") {
		t.Fatalf("error_message=%q leaked engine detail (provider-blind violation)", got.ErrorMessage)
	}
	if cancelled := checker.Cancelled(); len(cancelled) != 0 {
		t.Fatalf("expected no Cancel calls on the session-gone path (nothing left to stop), got %v", cancelled)
	}

	// Second pass: the now-failed task is no longer active, so it is never
	// checked again — proof this does not retry forever.
	callsBefore := checker.Calls()
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if got := checker.Calls(); got != callsBefore {
		t.Fatalf("expected 0 additional Status calls once the task is terminal, calls went from %d to %d", callsBefore, got)
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
