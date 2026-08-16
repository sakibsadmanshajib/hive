package agenttask

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// StatusChecker is the narrow surface the Poller needs from an Engine
// implementation. apps/control-plane/internal/agentengine.Engine already has
// a method with this exact signature (agenttask.Status is its own return
// type), so it satisfies this interface with no adapter code — Go
// interfaces are structural.
type StatusChecker interface {
	Status(ctx context.Context, sessionRef string) (status Status, resultSummary, errMessage string, err error)
}

// maxPollerBackoff caps the exponential backoff RunOnce failures grow the
// loop's interval to (see loop's doc comment).
const maxPollerBackoff = 5 * time.Minute

// ErrEngineSessionGone is a StatusChecker implementation's definitive answer
// that sessionRef no longer exists anywhere the engine can reach it — the
// launcher restarted and lost its in-memory session registry, and its own
// /status endpoint answers 404 for exactly that reason (see
// agentengine.Remote.post and agentengine.Engine.Status, which map their
// respective 404/engineapi.ErrUnknownSession cases onto this sentinel).
// Unlike every other StatusChecker error, this one is not retried at all: a
// session the engine has no memory of can never report progress again, so
// RunOnce fails the task immediately instead of leaving it queued/running
// forever or charging it against its failure budget first.
//
// The routine producer of this path on the demo box is a DEPLOY, not a rare
// crash: deploy-demo-box.yml runs scripts/install-agent-engine-host.sh
// unconditionally on every merge to main, which always stops and restarts
// the launcher unit, and every in-flight session's registry entry dies with
// it. So a merge to main now terminally fails every in-flight Cowork task
// within one poll interval of the restart completing, including one whose
// sandbox had already finished successfully but whose result the poller had
// not yet read — the truthful state there is "outcome unrecoverable," not
// "the task failed," which is why engineSessionGoneMessage below is worded
// the way it is rather than asserting failure. Mitigating this (a drain, or
// a registry that survives a restart) is tracked separately, issue #921;
// this fix intentionally does not build it.
var ErrEngineSessionGone = errors.New("agenttask: engine has no memory of this session")

// engineSessionGoneMessage is the customer-visible error_message persisted
// when ErrEngineSessionGone fires. Provider-blind, same rationale as
// Service's engineLaunchFailedMessage: no session reference, sandbox path,
// or engine detail ever reaches a customer-facing field. Worded as an
// unrecoverable outcome rather than an assertion of failure: the sandbox may
// well have finished the work before the launcher lost the reference to it,
// so "failed" would be stating something this code cannot actually know.
const engineSessionGoneMessage = "this task's outcome could not be recovered: its session was lost"

// maxTaskFailureDuration bounds, in wall-clock time rather than a raw pass
// count, how long a single task's status check (or, once it reports a real
// terminal status, the transition that records it) may keep failing before
// RunOnce gives up on that ONE task and fails it directly, regardless of
// what shape the error took. About 5 minutes, the same order of magnitude
// maxPollerBackoff used to cap the whole poller's shared interval at, now
// scoped to one task instead of every tenant's sweep.
//
// Expressed as a duration and converted to a pass count via
// Poller.taskFailureBudget (using the poller's OWN configured interval)
// rather than a fixed pass count, deliberately: HIVE_AGENT_TASK_POLL_INTERVAL
// is operator-tunable, and lowering it is the obvious reaction to this very
// incident. A fixed pass count
// would have silently shortened the real kill timeout in proportion — at a
// tuned 3s interval a fixed 20-pass budget would give up on a task in 60
// seconds instead of 5 minutes, against Cowork tasks that routinely run
// sixteen to twenty-two minutes, killing legitimately slow-to-report work
// instead of only genuinely dead sessions. Tying the budget to the
// interval keeps its documented ~5 minute meaning invariant regardless of
// how that knob is tuned.
//
// This exists because ErrEngineSessionGone only covers one definitive
// failure shape: a 404 from a launcher whose in-memory registry never had
// (or lost) the session. A sandbox killed some other way — OOM, an
// Apptainer crash, anything that does not go through Cancel or a normal
// terminal status — never reaches SandboxEngine.reap (it runs only on
// terminal status or Cancel), so its /status call fails forever too, but
// with a 502, not a 404 (apps/agent-engine/cmd/agent-engine/serve.go's
// default error-mapping branch). Same dead task as f9409763, a different,
// non-enumerable error shape. A per-task budget catches that case and any
// future one without needing to name every failure mode a StatusChecker
// implementation could return.
const maxTaskFailureDuration = 5 * time.Minute

// engineUnreachableAfterRetriesMessage is the customer-visible
// error_message persisted when a task exhausts its failure budget (see
// Poller.taskFailureBudget) without ErrEngineSessionGone ever firing.
// Provider-blind and worded the same unrecoverable-outcome way as
// engineSessionGoneMessage, for the same reason: repeated Status failures
// mean this code cannot learn what happened, not that the task is confirmed
// to have failed.
const engineUnreachableAfterRetriesMessage = "this task's outcome could not be recovered: its session stopped answering"

// budgetExceededCanceler is the narrow surface chargeFailureBudget needs to
// best-effort stop a session before giving up on its row: StatusChecker
// itself has no Cancel method (by design, per its own doc comment), but
// every concrete StatusChecker cmd/server/main.go's buildAgentEngine wires
// (agentengine.Remote for the socket arm, agentengine.Engine in-process) is
// also an agenttask.Engine, so it has one. Without this, exhausting the
// budget only marked the row failed and never told the engine to let go: a
// non-404 error means the launcher answered from its own handler, so the
// session is still live in SandboxEngine.sessions holding a concurrency
// slot (reap runs only from a terminal Status or Cancel), and once the row
// is terminal, Service.Cancel's own atomic guard returns ErrTerminalState
// before it ever reaches stopEngineSession — nothing else would ever free
// that slot, reproducing issue #886's leaked-slot symptom through a new
// door. Deliberately NOT used on the ErrEngineSessionGone path: there is
// nothing left to stop, the engine already said so.
type budgetExceededCanceler interface {
	Cancel(ctx context.Context, sessionRef string) error
}

// PollerConfig controls Poller behaviour.
type PollerConfig struct {
	// Interval between poll passes when the previous pass had no errors.
	// Zero defaults to 15s.
	Interval time.Duration
	// Logger; nil defaults to slog.Default().
	Logger *slog.Logger
}

// Poller periodically advances every active (queued/running, launched) task
// to its terminal state: lists them (Repository.ListActive), polls each
// one's engine status (StatusChecker.Status), and atomically transitions
// terminal ones (Repository.Transition). Mirrors
// apps/control-plane/internal/spendalerts.Runner's Start/Stop/RunOnce shape.
type Poller struct {
	repo     Repository
	checker  StatusChecker
	interval time.Duration
	logger   *slog.Logger

	// taskFailuresMu guards taskFailures. RunOnce is exported, so a caller
	// besides loop (which Start/Stop's own mutex serializes) could call it
	// concurrently; loop's own serialization guarantee protects loop against
	// itself, not this method against an arbitrary caller, and an
	// unsynchronized concurrent map write is a process-fatal throw with no
	// recover, not merely a data race. Cheap to close, so closed.
	taskFailuresMu sync.Mutex
	// taskFailures counts each task's CONSECUTIVE per-pass failures (see
	// taskFailureBudget).
	taskFailures map[uuid.UUID]int

	mu      sync.Mutex
	cancel  context.CancelFunc
	doneCh  chan struct{}
	started bool
}

// NewPoller builds a Poller. repo and checker must be non-nil.
func NewPoller(repo Repository, checker StatusChecker, cfg PollerConfig) *Poller {
	if repo == nil {
		panic("agenttask: nil repository")
	}
	if checker == nil {
		panic("agenttask: nil status checker")
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Poller{repo: repo, checker: checker, interval: interval, logger: logger, taskFailures: make(map[uuid.UUID]int)}
}

// taskFailureBudget converts maxTaskFailureDuration to a pass count using
// THIS poller's own configured interval, so the budget's documented ~5
// minute meaning does not silently drift when HIVE_AGENT_TASK_POLL_INTERVAL
// is tuned (see maxTaskFailureDuration's doc comment). At least 1: an
// interval tuned coarser than the duration itself still gets one retry
// before giving up, never zero.
func (p *Poller) taskFailureBudget() int {
	n := int(maxTaskFailureDuration / p.interval)
	if n < 1 {
		return 1
	}
	return n
}

// taskFailureCount returns id's current consecutive-failure streak, 0 if it
// has none.
func (p *Poller) taskFailureCount(id uuid.UUID) int {
	p.taskFailuresMu.Lock()
	defer p.taskFailuresMu.Unlock()
	return p.taskFailures[id]
}

// incrementTaskFailure counts one more consecutive failure against id and
// returns the new total.
func (p *Poller) incrementTaskFailure(id uuid.UUID) int {
	p.taskFailuresMu.Lock()
	defer p.taskFailuresMu.Unlock()
	p.taskFailures[id]++
	return p.taskFailures[id]
}

// clearTaskFailure forgets id's failure streak (a clean pass, a resolved
// terminal state, or the task no longer being active at all).
func (p *Poller) clearTaskFailure(id uuid.UUID) {
	p.taskFailuresMu.Lock()
	defer p.taskFailuresMu.Unlock()
	delete(p.taskFailures, id)
}

// pruneInactiveTaskFailures forgets every tracked task's streak that is not
// in active, so the map does not grow forever once a task stops showing up
// in ListActive (resolved this pass, by a concurrent Cancel, or anything
// else).
func (p *Poller) pruneInactiveTaskFailures(active map[uuid.UUID]bool) {
	p.taskFailuresMu.Lock()
	defer p.taskFailuresMu.Unlock()
	for id := range p.taskFailures {
		if !active[id] {
			delete(p.taskFailures, id)
		}
	}
}

// RunOnce performs exactly one poll pass: every active task gets exactly one
// StatusChecker.Status call and, if terminal, one Repository.Transition
// call.
//
// The returned error, when non-nil, is loop's sole backoff signal (see
// loop's doc comment) and means ListActive itself failed — a real
// database/connectivity problem, the only thing that is actually poller-wide.
// No per-task problem ever feeds it. Repository.ListActive is cross-tenant,
// so treating any single task's trouble as pass-level failure let one
// permanently broken task hold the whole poller's shared backoff at
// maxPollerBackoff for as long as that row existed, throttling poll cadence
// for every other tenant's tasks too (measured live 2026-08-16, task
// f9409763). Worse, the demo box's own quota (2 per user, 4 per tenant)
// makes a single active task the ORDINARY shape, not a corner case, so any
// rule keyed on a fraction of active tasks (an earlier version of this fix
// used errCount == len(tasks)) collapses back to "this one task" exactly
// when it matters most — caught in review before merge, not shipped.
//
// Instead, every task-level problem is scoped to that ONE task via
// pollTask/chargeFailureBudget: a StatusChecker error definitively
// identified as ErrEngineSessionGone fails the task immediately; anything
// else is retried per pass, up to taskFailureBudget consecutive times, after
// which RunOnce gives up on that task and fails it directly regardless of
// the error's shape (see maxTaskFailureDuration's doc comment for why a
// budget, not an enumeration, is what actually closes this class of bug). A
// Repository.Transition failure for an ALREADY-KNOWN terminal status (the
// engine told us the task succeeded or failed, but persisting that failed)
// is retried forever exactly as before this fix and is never charged
// against the budget or forced to a status we cannot confirm — that budget
// exists for "we cannot even ask what happened," not for "we know and can't
// write it yet."
func (p *Poller) RunOnce(ctx context.Context) (advanced int, err error) {
	tasks, err := p.repo.ListActive(ctx)
	if err != nil {
		return 0, fmt.Errorf("agenttask: poller list active: %w", err)
	}

	active := make(map[uuid.UUID]bool, len(tasks))
	var declaredDead int
	for _, t := range tasks {
		active[t.ID] = true
		res := p.pollTask(ctx, t)
		if res.advanced {
			advanced++
		}
		if res.declaredDead {
			declaredDead++
		}
	}

	// One line per pass, not one per task: the signature that actually
	// matters operationally (a launcher restart killed every tenant's
	// in-flight task at once, see ErrEngineSessionGone's doc comment) is
	// otherwise invisible in a log made of N separate per-task WARN lines
	// with no total anywhere.
	if declaredDead > 0 {
		p.logger.WarnContext(ctx, "agenttask: poller declared task(s) dead this pass",
			"declared_dead", declaredDead, "active_this_pass", len(tasks))
	}

	p.pruneInactiveTaskFailures(active)
	return advanced, nil
}

// pollResult is pollTask's outcome for one task.
type pollResult struct {
	// advanced reports whether the task reached a terminal state this pass,
	// for whatever reason: the engine's own report, or the poller giving up
	// on it (declaredDead below covers exactly the latter).
	advanced bool
	// declaredDead reports whether THIS pass is the one where the poller
	// itself decided the task is dead (ErrEngineSessionGone or an exhausted
	// failure budget) — never true for a task the engine reported
	// succeeded/failed/cancelled on its own.
	declaredDead bool
}

// pollTask advances one task and reports its outcome this pass (see
// pollResult). Every failure path here is scoped to t alone — nothing it
// does is visible to RunOnce's own return value, by design (see RunOnce's
// doc comment).
func (p *Poller) pollTask(ctx context.Context, t Task) pollResult {
	status, resultSummary, errMessage, statusErr := p.checker.Status(ctx, t.EngineSessionRef)
	if statusErr != nil {
		if errors.Is(statusErr, ErrEngineSessionGone) {
			p.logger.WarnContext(ctx, "agenttask: engine has no memory of this session, failing the task",
				"task_id", t.ID, "error", statusErr)
			ok := p.failTask(ctx, t, engineSessionGoneMessage)
			return pollResult{advanced: ok, declaredDead: ok}
		}
		ok := p.chargeFailureBudget(ctx, t, statusErr)
		return pollResult{advanced: ok, declaredDead: ok}
	}
	p.clearTaskFailure(t.ID) // a clean answer this pass, whatever it was

	if status != StatusSucceeded && status != StatusFailed && status != StatusCancelled {
		return pollResult{}
	}

	// Provider-blind boundary: errMessage came from StatusChecker, an
	// interface this package does not control the implementation of.
	// error_message is customer-visible (Handler.handleGet), so a raw
	// engine/provider detail must never reach it — log the real detail
	// server-side, persist a generic message instead.
	if errMessage != "" {
		p.logger.WarnContext(ctx, "agenttask: task failed, engine detail",
			"task_id", t.ID, "engine_detail", errMessage)
		errMessage = "agent task failed"
	}

	if _, transErr := p.repo.Transition(ctx, t.TenantID, t.UserID, t.ID, status, "", resultSummary, errMessage); transErr != nil {
		if errors.Is(transErr, ErrTerminalState) {
			// Lost a race with a concurrent Cancel (or a previous pass that
			// already advanced this task): already terminal, nothing left
			// to do.
			return pollResult{}
		}
		// A real database write failure for an ALREADY-KNOWN outcome:
		// retried next pass forever, exactly as before this fix. Not
		// charged against the failure budget — see RunOnce's doc comment.
		p.logger.WarnContext(ctx, "agenttask: poller transition failed, retrying next pass",
			"task_id", t.ID, "error", transErr)
		return pollResult{}
	}
	return pollResult{advanced: true} // the engine's own report, not the poller giving up
}

// chargeFailureBudget counts one more consecutive failure against t and, once
// that reaches taskFailureBudget, gives up on the task for good instead of
// retrying it forever (see maxTaskFailureDuration's doc comment for why this
// exists alongside ErrEngineSessionGone rather than instead of it).
func (p *Poller) chargeFailureBudget(ctx context.Context, t Task, cause error) (declaredDead bool) {
	count := p.incrementTaskFailure(t.ID)
	if count < p.taskFailureBudget() {
		p.logger.WarnContext(ctx, "agenttask: poller status check failed, retrying next pass",
			"task_id", t.ID, "consecutive_failures", count, "error", cause)
		return false
	}
	p.logger.WarnContext(ctx, "agenttask: task exceeded its failure budget, failing it",
		"task_id", t.ID, "consecutive_failures", count, "error", cause)

	// This branch (unlike ErrEngineSessionGone) means the launcher answered
	// from its own handler, so the session is very likely still live and
	// holding its concurrency slot (SandboxEngine.reap runs only from a
	// terminal Status or Cancel). Failing the row below without stopping the
	// engine first would leave that slot held forever: once the row is
	// terminal, Service.Cancel's own atomic guard returns ErrTerminalState
	// before it ever reaches stopEngineSession, so nothing else can free it.
	// Best-effort and before the Transition: if the session really did just
	// finish, Cancel is a documented no-op on it (agenttask.Engine's doc
	// comment), and if the Transition below then loses a race to a
	// concurrent Cancel or completion, that path's own stop attempt (or the
	// fact that a successful completion needs no stop at all) makes this one
	// redundant, not wrong.
	if canceler, ok := p.checker.(budgetExceededCanceler); ok {
		if err := canceler.Cancel(ctx, t.EngineSessionRef); err != nil {
			p.logger.WarnContext(ctx, "agenttask: best-effort session stop failed after exhausting the failure budget, its concurrency slot may still be held",
				"task_id", t.ID, "error", err)
		}
	}
	return p.failTask(ctx, t, engineUnreachableAfterRetriesMessage)
}

// failTask transitions t straight to StatusFailed with a provider-blind
// message. Deliberately does not clear t's failure streak when the
// Transition call itself fails with a real (non-ErrTerminalState) error: the
// streak is already at or past taskFailureBudget by the time this runs
// (chargeFailureBudget only calls it once the budget is exhausted, and
// ErrEngineSessionGone routes straight here on its own), so leaving the
// count where it is makes the next pass retry this same transition
// immediately rather than waiting out a fresh budget cycle first.
func (p *Poller) failTask(ctx context.Context, t Task, message string) (advanced bool) {
	if _, transErr := p.repo.Transition(ctx, t.TenantID, t.UserID, t.ID, StatusFailed, "", "", message); transErr != nil {
		if errors.Is(transErr, ErrTerminalState) {
			// Already resolved, e.g. by a concurrent Cancel: not a failure
			// of this attempt, forget the streak.
			p.clearTaskFailure(t.ID)
			return false
		}
		p.logger.WarnContext(ctx, "agenttask: poller transition failed, retrying next pass",
			"task_id", t.ID, "error", transErr)
		return false
	}
	p.clearTaskFailure(t.ID)
	return true
}

// Start launches the poll loop on a background goroutine. Subsequent Start
// calls are no-ops until Stop is called.
func (p *Poller) Start(parent context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	doneCh := make(chan struct{})
	p.cancel = cancel
	p.doneCh = doneCh
	p.started = true

	go p.loop(ctx, doneCh)
}

// Stop signals the loop to exit and waits for the in-flight pass to finish.
// Safe to call multiple times. started/cancel/doneCh are cleared only AFTER
// the wait: clearing them first would let a concurrent Start launch a
// second loop while the previous pass is still running (duplicate status
// checks, competing terminal transitions). The p.doneCh == doneCh check
// guards a concurrent second Stop from clearing state a subsequent
// Start/Stop cycle has already replaced.
func (p *Poller) Stop() {
	p.mu.Lock()
	if !p.started {
		p.mu.Unlock()
		return
	}
	cancel := p.cancel
	doneCh := p.doneCh
	p.mu.Unlock()

	cancel()
	<-doneCh

	p.mu.Lock()
	if p.doneCh == doneCh {
		p.started = false
		p.cancel = nil
		p.doneCh = nil
	}
	p.mu.Unlock()
}

// loop ticks RunOnce on p.interval, doubling the delay (capped at
// maxPollerBackoff) each pass that reports an error and resetting to
// p.interval on the first clean pass — a run of ListActive/database errors
// backs the poller off instead of hammering a struggling database at full
// frequency. An engine-side problem (the agent-engine daemon itself
// unreachable, one task's session gone, or anything else RunOnce's own doc
// comment scopes to a single task) never reaches this signal at all: it is
// bounded per task by its own failure budget instead, so it costs that one
// task some retries, never the whole poller's cadence.
func (p *Poller) loop(ctx context.Context, doneCh chan<- struct{}) {
	defer close(doneCh)

	consecutiveFailures := 0
	runPass := func() {
		if _, err := p.RunOnce(ctx); err != nil {
			consecutiveFailures++
		} else {
			consecutiveFailures = 0
		}
	}

	// Eager first pass so a service start advances already-active tasks
	// without waiting a full interval.
	runPass()

	timer := time.NewTimer(pollerBackoffDelay(p.interval, consecutiveFailures))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			runPass()
			timer.Reset(pollerBackoffDelay(p.interval, consecutiveFailures))
		}
	}
}

// pollerBackoffDelay returns the delay before the next pass:
// consecutiveFailures 0 → base; each further failure doubles it, capped at
// maxPollerBackoff. Pure function, kept separate from loop for testing
// without timers.
func pollerBackoffDelay(base time.Duration, consecutiveFailures int) time.Duration {
	d := base
	for i := 0; i < consecutiveFailures && d < maxPollerBackoff; i++ {
		d *= 2
	}
	if d > maxPollerBackoff {
		d = maxPollerBackoff
	}
	return d
}
