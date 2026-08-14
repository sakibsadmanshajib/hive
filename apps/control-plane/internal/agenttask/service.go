package agenttask

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Service validates and orchestrates task lifecycle operations on top of a
// Repository and an Engine. It is the single sanctioned write path; Handler
// never talks to Repository or Engine directly.
type Service struct {
	repo   Repository
	engine Engine

	// launches counts the background launch goroutines CreateTask starts.
	// Nothing on the request path waits on it; see WaitIdle.
	launches sync.WaitGroup
}

// NewService constructs a Service. repo must not be nil. A nil engine
// defaults to NotConfiguredEngine{} so callers that have not wired the
// agent-engine control channel yet still get well-defined (queued) behavior.
func NewService(repo Repository, engine Engine) *Service {
	if engine == nil {
		engine = NotConfiguredEngine{}
	}
	return &Service{repo: repo, engine: engine}
}

// engineUnavailableMessage is the customer-visible error_message persisted
// when ErrEngineNotConfigured fires. Deliberately generic: no provider name,
// internal hostname, or credential ever reaches a customer-facing field (see
// CLAUDE.md "Provider-blind errors"). The actionable detail (which
// HIVE_AGENT_ENGINE_* vars are empty) goes to the operator via the startup
// WARN in cmd/server/main.go's buildAgentEngine, not here.
const engineUnavailableMessage = "agent engine is not available on this deployment"

// engineLaunchFailedMessage is the customer-visible error_message persisted
// for every other Launch failure (anything but ErrEngineNotConfigured). Same
// provider-blind rationale as engineUnavailableMessage above: an arbitrary
// err.Error() from the Engine implementation can carry a provider name, an
// internal hostname, or an upstream error body, none of which may reach a
// customer-visible field. The real error still reaches the operator via the
// WarnContext log in the default case below, it just never reaches the task
// record.
const engineLaunchFailedMessage = "agent engine could not start the task"

// launchTimeout bounds one launch attempt. A cold sandbox has to mount a
// multi-gigabyte image and start a Python server inside it before it answers
// anything, so this is minutes rather than seconds.
const launchTimeout = 5 * time.Minute

// engineCancelTimeout bounds one stop call to the engine. Deliberately well
// under the 15 second budget edge-api's control-plane client allows
// (apps/edge-api/internal/agenttask.NewClient): a cancel that outlived that
// budget would answer the browser "failed" for a task that is in fact
// cancelled, which is the shape issue #881 is about. Stopping a session is a
// socket round trip plus a process kill, so seconds are generous; the
// launcher frees the slot even if this call's context dies first.
const engineCancelTimeout = 10 * time.Second

// CreateTask persists a new task and hands it to the agent-engine on a
// background goroutine, returning the persisted StatusQueued task as soon as
// the row exists (issue #881).
//
// Waiting for the launch inline was the bug: a launch is bounded at
// launchTimeout (five minutes) because a cold sandbox mount routinely takes
// tens of seconds, while edge-api's control-plane client gives up after 15
// seconds. Measured live on 2026-08-11, create answered the browser 500
// after 18.0 seconds for a task that went on to reach succeeded, so the user
// was told their work had failed while it was running and a retry started a
// second sandbox. Widening the client timeout to match the server's five
// minute bound was rejected: it makes an interactive request legitimately
// able to hang for five minutes and leaves every intermediate proxy free to
// cut it anyway. The queued row plus the existing poll path is what the code
// was already shaped for, and it is what the console already renders.
//
// A launch failure still reaches the caller, just on the next poll rather
// than in the create response: the background launch transitions the task to
// StatusFailed, so a task never sits queued forever with no signal.
func (s *Service) CreateTask(ctx context.Context, tenantID, userID uuid.UUID, pack Pack, instructions string) (Task, error) {
	if !pack.Valid() {
		return Task{}, ErrInvalidPack
	}

	t, err := s.repo.Create(ctx, tenantID, userID, pack, instructions)
	if err != nil {
		return Task{}, err
	}

	// Nothing bounds how many of these goroutines can be in flight, and the
	// launcher's quota does not: it gates the sandbox launch, which happens
	// inside the goroutine, after this row has already been inserted. There is
	// no rate limiter in front of POST /v1/agent/tasks either (verified in
	// apps/edge-api/cmd/server/main.go: the chain is unsupported-endpoint,
	// budget gate which is inert for JWT session traffic, auth selector,
	// metrics, compat headers, max bytes, plus a feature gate on the route;
	// authz.Limiter is only reached through authorizer.Authorize, which this
	// handler never calls). So an authenticated caller can spend rows,
	// goroutines and pool checkouts without a ceiling today. Tracked in issue
	// #900 rather than papered over with a comment claiming a control that is
	// not shipped.
	//
	// The launch runs on a context detached from the caller's. A launch
	// cold-starts a sandbox, which takes tens of seconds, and the browser tab
	// that submitted the task is free to go away in the middle of that.
	// Measured on the demo box: closing the tab cancelled the request
	// context, the launch died with "context canceled", and the task was
	// recorded as failed for a reason that had nothing to do with the task.
	// Worse, the Transition calls would inherit the same dead context and
	// fail too, leaving the row queued forever.
	opCtx := context.WithoutCancel(ctx)
	s.background(func() { s.launch(opCtx, t) })

	return t, nil
}

// background runs work on a goroutine tracked by WaitIdle, recovering any
// panic so one bad launch or stop cannot take the process down. Before this
// work ran on its own goroutine it ran inside the HTTP handler, where
// net/http's per-connection recover contained a panic to one connection; a
// bare goroutine would instead take the whole process down, every tenant with
// it. This recover is the outer net only: launch does its own task-level
// recovery, because only launch knows the session reference that has to be
// stopped.
func (s *Service) background(work func()) {
	s.launches.Add(1)
	go func() {
		defer s.launches.Done()
		defer func() {
			if r := recover(); r != nil {
				slog.Default().Error("agenttask: recovered panic in background work",
					"panic", r, "stack", string(debug.Stack()))
			}
		}()
		work()
	}()
}

// safely runs f and swallows any panic it raises, logging it against what.
// Only for use from inside a recovery path: a panic raised there is a fresh
// panic in an already-unwinding deferred function, with no recover above it,
// so it would kill the process. That matters most when the original panic came
// from a shared dependency (a nil pool, a driver fault), because then the
// recovery handler re-enters the very code that just failed and the second
// panic is the deterministic one.
func safely(what string, f func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Default().Error("agenttask: panic while "+what+", giving up on it", "panic", r)
		}
	}()
	f()
}

// launch performs one launch attempt and records its outcome. Runs on a
// background goroutine; it never returns anything to the caller of
// CreateTask, so every failure path ends in either a persisted status or an
// operator-visible log line.
func (s *Service) launch(ctx context.Context, t Task) {
	launchCtx, cancel := context.WithTimeout(ctx, launchTimeout)
	defer cancel()

	// sessionRef is declared before the recover that reads it: a panic between
	// a successful Launch and the transition that records the reference would
	// otherwise strand a live sandbox whose reference only the panicking frame
	// ever held, and nothing else could ever stop it. Stopping comes first
	// because it frees the concurrency slot without touching the database,
	// which is the dependency most likely to have caused the panic.
	var sessionRef string
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		slog.Default().Error("agenttask: recovered panic during launch",
			"task_id", t.ID, "panic", r, "stack", string(debug.Stack()))
		safely("stopping the session of a panicking launch", func() {
			s.stopEngineSession(ctx, t.ID, sessionRef)
		})
		safely("recording a panicking launch as failed", func() {
			s.recordLaunchFailure(ctx, t, engineLaunchFailedMessage)
		})
	}()

	sessionRef, err := s.engine.Launch(launchCtx, t)
	switch {
	case err == nil:
		if _, terr := s.repo.Transition(ctx, t.TenantID, t.UserID, t.ID, StatusRunning, sessionRef, "", ""); terr != nil {
			// The session started but its reference could not be recorded, so
			// nothing can ever poll or stop it and it would hold its
			// concurrency slot for its full natural life. Stop it here.
			s.stopEngineSession(ctx, t.ID, sessionRef)

			// ErrTerminalState is the expected case: a cancel won the race
			// while this launch was still in flight, and it had no session
			// reference to stop at the time, so this goroutine owns the
			// teardown. The task's terminal state is already correct.
			if errors.Is(terr, ErrTerminalState) {
				return
			}
			// Anything else is a real database failure, and leaving the row
			// queued with no session reference would strand it: ListActive
			// excludes exactly that shape, so the poller would never touch it
			// and the customer would watch a task that can never move and
			// carries no error. Record the failure instead. Best effort by
			// definition, since the same database just refused a write.
			slog.Default().WarnContext(ctx, "agenttask: could not record a launched session, stopped it and failing the task",
				"task_id", t.ID, "error", terr)
			s.recordLaunchFailure(ctx, t, engineLaunchFailedMessage)
		}
	case errors.Is(err, ErrEngineNotConfigured):
		s.recordLaunchFailure(ctx, t, engineUnavailableMessage)
	default:
		// Known gap, deliberately not closed here (issue #899). A Launch that
		// fails in transport rather than at the launcher (the response is lost,
		// launchTimeout fires, or this process dies mid-deploy) can leave a
		// session that the launcher registered and this process never learned
		// the reference for. That session holds its slots until the launcher
		// restarts, because reap only runs from Status or Cancel and the
		// poller's input requires a non-empty engine_session_ref. Closing it
		// needs a way to re-derive the reference, which only the launcher can
		// offer (a lookup by task id, or its own orphan sweep). The row is
		// still recorded failed here, which is the half this service owns.
		slog.Default().WarnContext(ctx, "agenttask: launch failed, engine detail",
			"task_id", t.ID, "error", err)
		s.recordLaunchFailure(ctx, t, engineLaunchFailedMessage)
	}
}

// recordLaunchFailure persists a failed launch. ErrTerminalState is expected
// and ignored: the user cancelled while the launch was failing, and their
// cancellation is the truthful record.
func (s *Service) recordLaunchFailure(ctx context.Context, t Task, message string) {
	if _, err := s.repo.Transition(ctx, t.TenantID, t.UserID, t.ID, StatusFailed, "", "", message); err != nil &&
		!errors.Is(err, ErrTerminalState) {
		slog.Default().WarnContext(ctx, "agenttask: could not record a failed launch",
			"task_id", t.ID, "error", err)
	}
}

// stopEngineSession stops one launcher session, which is what releases the
// concurrency slot that session holds (issue #886). Errors are logged and
// never returned: by the time this runs the task's terminal state is already
// recorded, and a failure here is an operator problem (a leaked slot), not
// something the customer can act on or should see as a failed request. The
// engine detail goes to the log only, never to a customer-visible field.
func (s *Service) stopEngineSession(ctx context.Context, taskID uuid.UUID, sessionRef string) {
	if sessionRef == "" {
		return // never launched, so nothing holds a slot
	}
	stopCtx, done := context.WithTimeout(context.WithoutCancel(ctx), engineCancelTimeout)
	defer done()
	if err := s.engine.Cancel(stopCtx, sessionRef); err != nil {
		slog.Default().WarnContext(stopCtx, "agenttask: engine cancel failed, the session may still hold its concurrency slot",
			"task_id", taskID, "error", err)
	}
}

// WaitIdle blocks until every background launch and engine stop this Service
// started has finished. Nothing on the request path calls it; it exists so
// tests can assert on a settled task, and so a drain could let in-flight work
// record its outcome before the process exits.
//
// Deliberately not wired into cmd/server's shutdown, and the shutdown window
// this leaves is genuinely worse than before, so it is written down rather
// than glossed. Previously the launch ran inside the HTTP handler, so
// srv.Shutdown's 10 second budget (cmd/server/main.go) waited on that
// connection and an in-flight launch had up to 10 seconds of grace. The
// handler now returns as soon as the row is persisted, so Shutdown has
// nothing to wait for and the background goroutine is killed with
// approximately none.
//
// A task stranded that way sits queued with no engine_session_ref, which
// Repository.ListActive excludes, so the poller never advances it. Waiting on
// WaitIdle in Shutdown is not the fix: a launch is bounded at launchTimeout
// (five minutes) while container stop is bounded far lower, so it would only
// trade a clean exit for a SIGKILL at the same point. The fix, when a
// mid-deploy stranded task actually shows up, is a sweep that fails queued
// rows older than launchTimeout, which needs a change to the
// agent_tasks_list_active() function and therefore a migration. Not built here
// (ponytail), and named so the next person does not reason from a false
// premise.
func (s *Service) WaitIdle() { s.launches.Wait() }

// Get returns one task, scoped to (tenantID, userID) so a task started by
// one user is never resumable by a different user in the same tenant.
func (s *Service) Get(ctx context.Context, tenantID, userID, id uuid.UUID) (Task, error) {
	return s.repo.Get(ctx, tenantID, userID, id)
}

// List returns every task userID started within tenantID, newest first —
// the read path that makes a task started in one web session visible from
// another web session for the same user.
func (s *Service) List(ctx context.Context, tenantID, userID uuid.UUID) ([]Task, error) {
	return s.repo.List(ctx, tenantID, userID)
}

// Cancel transitions a task to StatusCancelled and stops the launcher
// session behind it. Only reachable from StatusQueued or StatusRunning; a
// task already in a terminal state returns ErrTerminalState. No read-then-act
// check here — Repository.Transition's UPDATE carries the "not already
// terminal" precondition atomically, so a concurrent engine callback
// finishing the task can never be clobbered by a racing Cancel (or vice
// versa).
//
// The order matters. The row transition runs first because it is the atomic
// gate: exactly one caller can win it, so the engine is asked to stop a
// session exactly once. A double cancel, and a cancel that lost the race to a
// completion the poller already recorded, both fail that gate and return here
// without touching the engine (in the completion case the launcher has
// already reaped that session itself). Stopping the engine first would risk
// killing a sandbox for a task the database then reports as succeeded.
//
// Stopping the session is the point (issue #886): the concurrency slot is
// held by the live sandbox and released by the launcher when the session
// ends, so a cancel that only wrote a database row left the slot held until
// the sandbox finished on its own. Measured on the demo box, that was about
// sixteen minutes, and two cancels exhausted the user's ceiling and refused
// every subsequent create.
func (s *Service) Cancel(ctx context.Context, tenantID, userID, id uuid.UUID) (Task, error) {
	cancelled, err := s.repo.Transition(ctx, tenantID, userID, id, StatusCancelled, "", "", "")
	if err != nil {
		return Task{}, err
	}
	// An empty EngineSessionRef means the launch has not reported back yet;
	// the in-flight launch goroutine sees this task is already terminal and
	// tears its own session down (see launch).
	//
	// The stop runs in the background for the same reason create no longer
	// waits on a launch (issue #881). Blocking here put up to
	// engineCancelTimeout (10 seconds) plus the database write inside a request
	// edge-api abandons at 15 seconds, and the box where this matters is by
	// definition the loaded one. The caller's answer does not depend on the
	// stop anyway: the cancellation is already committed, and a stop failure is
	// logged for an operator rather than returned. Tests wait on WaitIdle.
	sessionRef := cancelled.EngineSessionRef
	stopCtx := context.WithoutCancel(ctx)
	s.background(func() { s.stopEngineSession(stopCtx, id, sessionRef) })
	return cancelled, nil
}
