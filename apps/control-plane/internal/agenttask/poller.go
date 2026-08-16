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
// forever or charging it against maxTaskFailureBudget first.
var ErrEngineSessionGone = errors.New("agenttask: engine has no memory of this session")

// engineSessionGoneMessage is the customer-visible error_message persisted
// when ErrEngineSessionGone fires. Provider-blind, same rationale as
// Service's engineLaunchFailedMessage: no session reference, sandbox path,
// or engine detail ever reaches a customer-facing field.
const engineSessionGoneMessage = "agent task session is no longer reachable"

// maxTaskFailureBudget bounds how many consecutive passes a single task's
// status check (or, once it reports a real terminal status, the transition
// that records it) may fail before RunOnce gives up on that ONE task and
// fails it directly, regardless of what shape the error took. 20 passes is
// about 5 minutes at the default 15s interval, the same order of magnitude
// maxPollerBackoff used to cap the whole poller's shared interval at, now
// scoped to one task instead of every tenant's sweep.
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
const maxTaskFailureBudget = 20

// engineUnreachableAfterRetriesMessage is the customer-visible
// error_message persisted when a task exhausts maxTaskFailureBudget without
// ErrEngineSessionGone ever firing. Provider-blind, same rationale as
// engineSessionGoneMessage.
const engineUnreachableAfterRetriesMessage = "agent task could not reach its session after repeated attempts"

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

	// taskFailures counts each task's CONSECUTIVE per-pass failures (see
	// maxTaskFailureBudget). Only ever touched from RunOnce, which loop
	// guarantees is never running concurrently with itself (Start/Stop's own
	// mutex serializes passes), so this needs no lock of its own.
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
// else is retried per pass, up to maxTaskFailureBudget consecutive times,
// after which RunOnce gives up on that task and fails it directly regardless
// of the error's shape (see maxTaskFailureBudget's doc comment for why a
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
	for _, t := range tasks {
		active[t.ID] = true
		if p.pollTask(ctx, t) {
			advanced++
		}
	}

	// Forget the failure streak of any task no longer active (resolved this
	// pass, by a concurrent Cancel, or anything else) so the map does not
	// grow forever.
	for id := range p.taskFailures {
		if !active[id] {
			delete(p.taskFailures, id)
		}
	}
	return advanced, nil
}

// pollTask advances one task and reports whether it reached a terminal
// state this pass. Every failure path here is scoped to t alone — nothing
// it does is visible to RunOnce's own return value, by design (see
// RunOnce's doc comment).
func (p *Poller) pollTask(ctx context.Context, t Task) (advanced bool) {
	status, resultSummary, errMessage, statusErr := p.checker.Status(ctx, t.EngineSessionRef)
	if statusErr != nil {
		if errors.Is(statusErr, ErrEngineSessionGone) {
			p.logger.WarnContext(ctx, "agenttask: engine has no memory of this session, failing the task",
				"task_id", t.ID, "error", statusErr)
			return p.failTask(ctx, t, engineSessionGoneMessage)
		}
		return p.chargeFailureBudget(ctx, t, statusErr)
	}
	delete(p.taskFailures, t.ID) // a clean answer this pass, whatever it was

	if status != StatusSucceeded && status != StatusFailed && status != StatusCancelled {
		return false
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
			return false
		}
		// A real database write failure for an ALREADY-KNOWN outcome:
		// retried next pass forever, exactly as before this fix. Not
		// charged against the failure budget — see RunOnce's doc comment.
		p.logger.WarnContext(ctx, "agenttask: poller transition failed, retrying next pass",
			"task_id", t.ID, "error", transErr)
		return false
	}
	return true
}

// chargeFailureBudget counts one more consecutive failure against t and, once
// that reaches maxTaskFailureBudget, gives up on the task for good instead of
// retrying it forever (see maxTaskFailureBudget's doc comment for why this
// exists alongside ErrEngineSessionGone rather than instead of it).
func (p *Poller) chargeFailureBudget(ctx context.Context, t Task, cause error) (advanced bool) {
	p.taskFailures[t.ID]++
	if p.taskFailures[t.ID] < maxTaskFailureBudget {
		p.logger.WarnContext(ctx, "agenttask: poller status check failed, retrying next pass",
			"task_id", t.ID, "consecutive_failures", p.taskFailures[t.ID], "error", cause)
		return false
	}
	p.logger.WarnContext(ctx, "agenttask: task exceeded its failure budget, failing it",
		"task_id", t.ID, "consecutive_failures", p.taskFailures[t.ID], "error", cause)
	return p.failTask(ctx, t, engineUnreachableAfterRetriesMessage)
}

// failTask transitions t straight to StatusFailed with a provider-blind
// message. Deliberately does not clear t's failure streak when the
// Transition call itself fails with a real (non-ErrTerminalState) error: the
// streak is already at or past maxTaskFailureBudget by the time this runs
// (chargeFailureBudget only calls it once the budget is exhausted, and
// ErrEngineSessionGone routes straight here on its own), so leaving the
// count where it is makes the next pass retry this same transition
// immediately rather than waiting out a fresh maxTaskFailureBudget cycle
// first.
func (p *Poller) failTask(ctx context.Context, t Task, message string) (advanced bool) {
	if _, transErr := p.repo.Transition(ctx, t.TenantID, t.UserID, t.ID, StatusFailed, "", "", message); transErr != nil {
		if errors.Is(transErr, ErrTerminalState) {
			// Already resolved, e.g. by a concurrent Cancel: not a failure
			// of this attempt, forget the streak.
			delete(p.taskFailures, t.ID)
			return false
		}
		p.logger.WarnContext(ctx, "agenttask: poller transition failed, retrying next pass",
			"task_id", t.ID, "error", transErr)
		return false
	}
	delete(p.taskFailures, t.ID)
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
// bounded per task by maxTaskFailureBudget instead, so it costs that one
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
