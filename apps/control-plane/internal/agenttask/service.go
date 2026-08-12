package agenttask

import (
	"context"
	"errors"
	"log/slog"
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
	// Nothing on the request path waits on it; see WaitForLaunches.
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

	// The launch runs on a context detached from the caller's. A launch
	// cold-starts a sandbox, which takes tens of seconds, and the browser tab
	// that submitted the task is free to go away in the middle of that.
	// Measured on the demo box: closing the tab cancelled the request
	// context, the launch died with "context canceled", and the task was
	// recorded as failed for a reason that had nothing to do with the task.
	// Worse, the Transition calls would inherit the same dead context and
	// fail too, leaving the row queued forever.
	opCtx := context.WithoutCancel(ctx)
	s.launches.Add(1)
	go func() {
		defer s.launches.Done()
		s.launch(opCtx, t)
	}()

	return t, nil
}

// launch performs one launch attempt and records its outcome. Runs on a
// background goroutine; it never returns anything to the caller of
// CreateTask, so every failure path ends in either a persisted status or an
// operator-visible log line.
func (s *Service) launch(ctx context.Context, t Task) {
	launchCtx, cancel := context.WithTimeout(ctx, launchTimeout)
	defer cancel()

	sessionRef, err := s.engine.Launch(launchCtx, t)
	switch {
	case err == nil:
		if _, terr := s.repo.Transition(ctx, t.TenantID, t.UserID, t.ID, StatusRunning, sessionRef, "", ""); terr != nil {
			// The session started but its reference could not be recorded, so
			// nothing can ever poll or stop it and it would hold its
			// concurrency slot for its full natural life. Stop it here.
			// ErrTerminalState is the expected case: a cancel won the race
			// while this launch was still in flight, and it had no session
			// reference to stop at the time, so this goroutine owns the
			// teardown.
			if !errors.Is(terr, ErrTerminalState) {
				slog.Default().WarnContext(ctx, "agenttask: could not record a launched session, stopping it",
					"task_id", t.ID, "error", terr)
			}
			s.stopEngineSession(ctx, t.ID, sessionRef)
		}
	case errors.Is(err, ErrEngineNotConfigured):
		s.recordLaunchFailure(ctx, t, engineUnavailableMessage)
	default:
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

// WaitForLaunches blocks until every background launch this Service started
// has finished. Nothing on the request path calls it; it exists so tests can
// assert on a settled task, and so a graceful drain can let in-flight
// launches record their outcome before the process exits.
func (s *Service) WaitForLaunches() { s.launches.Wait() }

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
	s.stopEngineSession(ctx, id, cancelled.EngineSessionRef)
	return cancelled, nil
}
