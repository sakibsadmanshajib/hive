package agenttask

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"strings"
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
	// src is the optional event/files surface (nil when no engine arm is
	// wired). See WithEventSource.
	src EventSource
	// creds mints the per-task gateway credential the sandbox spends, and
	// revokes it when the task ends. Never nil: NewService defaults it to
	// notConfiguredCredentials, which refuses every launch rather than let a
	// sandbox fall back to the launcher's process-wide key (#1507).
	creds TaskCredentials

	// launches counts the background launch goroutines CreateTask starts.
	// Nothing on the request path waits on it; see WaitIdle.
	launches sync.WaitGroup
}

// NewService constructs a Service. repo must not be nil. A nil engine
// defaults to NotConfiguredEngine{} so callers that have not wired the
// agent-engine control channel yet still get well-defined (queued) behavior.
//
// WithEventSource is the only option today; see its doc comment.
func NewService(repo Repository, engine Engine, opts ...ServiceOption) *Service {
	if engine == nil {
		engine = NotConfiguredEngine{}
	}
	s := &Service{repo: repo, engine: engine, creds: notConfiguredCredentials{}}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ServiceOption configures optional Service wiring.
type ServiceOption func(*Service)

// WithEventSource attaches the engine's event/files surface. Nil (the
// default, and what every deployment without a configured agent-engine gets)
// makes Files answer an empty listing rather than fail: the read route exists
// wherever the task routes exist, and "no events source" is a deployment
// posture, not a caller error.
func WithEventSource(src EventSource) ServiceOption {
	return func(s *Service) { s.src = src }
}

// WithTaskCredentials attaches the per-task credential seam (#1507). Without
// it a Service mints nothing and every launch fails closed, the same shape
// edge-api's sessionbilling uses for a handler built without its accounting
// seam: a surface that cannot attribute what a tenant spends must not launch
// the thing that spends it. A nil argument is ignored rather than installed,
// so a caller that wires this optionally cannot silently disarm it.
func WithTaskCredentials(creds TaskCredentials) ServiceOption {
	return func(s *Service) {
		if creds != nil {
			s.creds = creds
		}
	}
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

// cancelFlushTimeout bounds the event flush a cancel does before it commits.
// Deliberately short and well inside the 15 second budget edge-api's
// control-plane client allows: the cancellation itself is the answer the
// caller is waiting for, so a sandbox too wedged to say what it did in this
// long must not be able to make a cancel look like a failure. Missing the last
// steps of a run somebody stopped is the degraded outcome; not cancelling it
// is the wrong one.
const cancelFlushTimeout = 3 * time.Second

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
// bearerJWT is the task's own user's bearer JWT (edge-api's handler is the
// only caller with it; see Task.BearerJWT's doc comment). Threaded straight
// through to the launch goroutine and never persisted by s.repo.Create.
func (s *Service) CreateTask(ctx context.Context, tenantID, userID uuid.UUID, pack Pack, instructions string, projectID uuid.UUID, bearerJWT string) (Task, error) {
	// No pack named means "you work out which kind of task this is" (issue
	// #1623), which is what the composer sends now that a customer no longer
	// picks between two words that describe a system prompt. Resolved here
	// rather than in edge-api or in the browser because this is the one point
	// every caller routes through, so no client can end up with a different
	// answer, and because the value has to be concrete before it reaches the
	// repository: the column has a CHECK constraint and the launcher fails
	// closed on a pack it cannot read.
	//
	// A pack that is neither empty nor real is still refused. That is a broken
	// client, not a caller declining to choose, and guessing on its behalf
	// would hide the bug.
	//
	// Trimmed here rather than only at the edge, so that "the one point every
	// caller routes through" is true of the whitespace normalisation as well
	// as the inference. edge-api trims before forwarding, so a customer never
	// saw the difference, but the internal surface took a pack of " " to the
	// CHECK constraint and answered ErrInvalidPack for an input the public
	// path infers, which is two surfaces disagreeing about one value.
	if pack = Pack(strings.TrimSpace(string(pack))); pack == "" {
		pack = InferPack(instructions)
	}
	if !pack.Valid() {
		return Task{}, ErrInvalidPack
	}

	t, err := s.repo.Create(ctx, tenantID, userID, pack, instructions, projectID)
	if err != nil {
		return Task{}, err
	}
	t.BearerJWT = bearerJWT

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
	var (
		sessionRef  string
		teardownRun bool // this launch already attempted its own teardown
	)
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		// Residual, deliberately not wrapped: a panic from this logging call
		// (or from debug.Stack) escapes into background's recover, which logs
		// through the same slog call, so the two nets are not independent for
		// that one case. Reaching it needs a slog handler that panics, and
		// control-plane installs none: there is no slog.SetDefault in
		// cmd/server, so this is the stdlib text handler, and fmt recovers from
		// a panicking String or Error on the panic value itself. Revisit if a
		// structured handler is ever installed.
		slog.Default().Error("agenttask: recovered panic during launch",
			"task_id", t.ID, "panic", r, "stack", string(debug.Stack()))
		// Skipped when the launch already tore its own session down: repeating
		// it is harmless for accounting (quota's release is a sync.Once and
		// reap is idempotent) but it logs a leaked-slot warning for a slot that
		// was correctly freed, because the control socket directory is gone by
		// then and Interrupt fails.
		if !teardownRun {
			safely("stopping the session of a panicking launch", func() {
				s.stopEngineSession(ctx, t.ID, sessionRef)
			})
		}
		safely("recording a panicking launch as failed", func() {
			s.recordLaunchFailure(ctx, t, engineLaunchFailedMessage)
		})
		// No separate revoke here: recordLaunchFailure above already revokes,
		// gated on t.LLMAPIKey, which this closure observes because launch
		// assigns it on the same variable.
	}()

	// The sandbox must spend a credential that resolves to THIS task's tenant,
	// or it must not start at all (#1507). Falling back to the launcher's
	// process-wide key is what made every tenant's agent inference settle
	// against one Hive-owned account and charge the customer nothing, so a
	// mint failure fails the task instead. Fail-closed, per D-034: never serve
	// work the authorizing accounting step did not cover.
	secret, err := s.creds.Mint(launchCtx, t)
	if err != nil {
		// No revocation attempt here, deliberately. Nothing was minted, so
		// there is nothing to destroy, and calling revoke anyway would raise
		// ErrCredentialUnaccountedFor for a task that never had a credential.
		// That report has to mean something, and a benign path that raises it
		// constantly is how it stops meaning anything.
		//
		// The error carries no secret: generateSecret returns "" on failure and
		// the raw secret is never placed in an error value, so this chain is at
		// worst "agenttask: mint task credential: apikeys: create: <db error>".
		// task_id doubles as the credential's id, which is an identifier the
		// customer already has, never a secret. The customer-visible field gets
		// engineLaunchFailedMessage, which names nothing.
		slog.Default().ErrorContext(ctx, "agenttask: could not mint the task's gateway credential, refusing to launch",
			"task_id", t.ID, "tenant_id", t.TenantID, "error", err)
		s.recordLaunchFailure(ctx, t, engineLaunchFailedMessage)
		return
	}
	// Assigned to t, which the deferred recover above closes over, so that
	// panic path can tell "a credential exists and must be revoked" from
	// "nothing was ever minted".
	t.LLMAPIKey = secret

	sessionRef, err = s.engine.Launch(launchCtx, t)
	switch {
	case err == nil:
		if _, terr := s.repo.Transition(ctx, t.TenantID, t.UserID, t.ID, StatusRunning, sessionRef, "", ""); terr != nil {
			// The session started but its reference could not be recorded, so
			// nothing can ever poll or stop it and it would hold its
			// concurrency slot for its full natural life. Stop it here.
			teardownRun = true
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
	// Gated on a credential having actually been minted, and the gate is here
	// rather than inside revokeTaskCredential because Cancel passes a
	// repository-loaded Task whose LLMAPIKey is always empty by design and
	// still has to revoke.
	//
	// Without it, launch's own mint-failure branch reaches this function with an
	// empty LLMAPIKey, the revoke finds no key, and the result is an
	// ErrCredentialUnaccountedFor ERROR for a task that never had a credential.
	// ErrNoBillingAccount is an ordinary mint failure, so that alert would fire
	// falsely on every one of them, and a reason string built to catch #1507
	// would become noise. That is the failure this PR exists to prevent, so it
	// is not allowed to be introduced by the fix for it.
	//
	// When a credential DOES exist it is revoked even on the one branch where
	// the sandbox may still be alive: a transport-level Launch failure can leave
	// a session the launcher registered and this process never learned the
	// reference for (#899), so there is nothing to stop first. That is the right
	// direction, not an oversight. The row is now recorded FAILED, so the
	// customer has been told this task produced nothing, and an orphaned sandbox
	// keeping its credential would go on spending that customer's credits on
	// work they will never be shown. An auth error that kills it is the cheaper
	// outcome, and its concurrency slot is #899's problem either way.
	if t.LLMAPIKey != "" {
		revokeTaskCredential(ctx, s.creds, t)
	}
}

// revokeTaskCredential ends one task's gateway credential, best effort.
//
// Package-scoped because the two writers that take a task terminal live in
// different types here: Service (a launch that failed, a cancel) and Poller (a
// run that finished, or that the poller declared dead). Both call this rather
// than keeping their own copy, so one place decides what a revocation failure
// means. Not exported: an exported symbol is a promise to callers who do not
// exist, and on a credential-destroying function it is a surface someone
// eventually calls from the wrong place.
//
// Never returns an error and never blocks a terminal transition. By the time it
// runs the task's terminal state is already recorded, so a failure here is an
// operator problem rather than something the customer can act on. nil creds is
// the poller's unwired posture and is silently a no-op; a Service always has a
// non-nil one.
//
// The two failure shapes are logged at different levels on purpose. Anything
// else is a WARN, because the credential's expiry still ends it. A credential
// that could not be FOUND is an ERROR with its own reason string, because that
// one means a live, spendable credential exists on a customer's billing account
// and this process no longer knows how to stop it. That is the outcome #1507 is
// made of, so it gets a signal loud enough that the next occurrence is not
// discovered weeks later in a ledger.
func revokeTaskCredential(ctx context.Context, creds TaskCredentials, t Task) {
	if creds == nil {
		return
	}
	revokeCtx, done := context.WithTimeout(context.WithoutCancel(ctx), credentialRevokeTimeout)
	defer done()
	err := creds.Revoke(revokeCtx, t)
	switch {
	case err == nil, errors.Is(err, ErrTaskCredentialsNotConfigured):
		return
	case errors.Is(err, ErrCredentialUnaccountedFor):
		slog.Default().ErrorContext(revokeCtx, "agenttask: a task credential could not be found to revoke, so it is still live and still spendable",
			"reason", "agent_task_credential_unaccounted_for",
			"task_id", t.ID, "tenant_id", t.TenantID, "error", err)
	default:
		slog.Default().WarnContext(revokeCtx, "agenttask: could not revoke the task's gateway credential, it stays live until it expires",
			"task_id", t.ID, "tenant_id", t.TenantID, "error", err)
	}
}

// credentialRevokeTimeout bounds one revocation. It is two database writes and
// a cache invalidation, so seconds are generous; the expiry is what covers a
// revocation that times out anyway.
const credentialRevokeTimeout = 10 * time.Second

// stopEngineSession stops one launcher session, which is what releases the
// concurrency slot that session holds (issue #886). Errors are logged and
// never returned: by the time this runs the task's terminal state is already
// recorded, and a failure here is an operator problem (a leaked slot), not
// something the customer can act on or should see as a failed request. The
// engine detail goes to the log only, never to a customer-visible field.
//
// The warning carries session_ref because on two paths that reach it (a cancel
// that won the race before the launch reported back, and the panic path above)
// the reference exists nowhere else by then: the row's engine_session_ref was
// never written, the poller cannot see the task, and the launcher's registry is
// in memory with no listing endpoint. Without it the only remedy left is
// restarting the launcher, which drops every other tenant's live sandbox too.
func (s *Service) stopEngineSession(ctx context.Context, taskID uuid.UUID, sessionRef string) {
	if sessionRef == "" {
		return // never launched, so nothing holds a slot
	}
	stopCtx, done := context.WithTimeout(context.WithoutCancel(ctx), engineCancelTimeout)
	defer done()
	err := s.engine.Cancel(stopCtx, sessionRef)
	if err == nil {
		return
	}
	if errors.Is(err, ErrEngineSessionGone) {
		// The engine already has no memory of this session (its in-memory
		// registry lost it, e.g. a launcher restart), so it holds no
		// concurrency slot for it either: the quota manager lives in the
		// same in-memory state as the session registry
		// (apps/agent-engine/internal/engine.SandboxEngine). Nothing leaked,
		// nothing for an operator to chase — warning here would point them
		// at a slot that does not exist, on exactly the path (issue #886)
		// where leaked slots are the thing they've been trained to hunt.
		return
	}
	slog.Default().WarnContext(stopCtx, "agenttask: engine cancel failed, the session may still hold its concurrency slot",
		"task_id", taskID, "session_ref", sessionRef, "error", err)
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

// Events returns one task's event rows strictly newer than afterSeq. Scoped
// exactly like Get: a task belonging to a different user is ErrNotFound, so
// cross-user reads 404 instead of leaking existence. The cursor was validated
// by the HTTP layer; this method trusts its inputs the way Get does.
func (s *Service) Events(ctx context.Context, tenantID, userID, id uuid.UUID, afterSeq int64, limit int) ([]TaskEvent, error) {
	if _, err := s.repo.Get(ctx, tenantID, userID, id); err != nil {
		return nil, err
	}
	return s.repo.ListEvents(ctx, tenantID, userID, id, afterSeq, limit)
}

// Files lists the running session's workspace directory (name, size, mtime).
// Same scoping as Events. A task that never launched, or whose session is
// gone, answers an empty listing rather than an error: the files route is
// best-effort by nature and the caller's real signal about liveness is the
// task's status.
func (s *Service) Files(ctx context.Context, tenantID, userID, id uuid.UUID) ([]WorkspaceFile, error) {
	t, err := s.repo.Get(ctx, tenantID, userID, id)
	if err != nil {
		return nil, err
	}
	if s.src == nil || t.EngineSessionRef == "" {
		return []WorkspaceFile{}, nil
	}
	files, err := s.src.Files(ctx, t.EngineSessionRef)
	if err != nil {
		// The launcher being down or the session having been reaped is not a
		// customer-visible failure mode: log server-side, answer empty.
		slog.Default().WarnContext(ctx, "agenttask: workspace listing unavailable",
			"task_id", t.ID, "error", err)
		return []WorkspaceFile{}, nil
	}
	return files, nil
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
	// Before the transition, for the reason the poller flushes before its own
	// (issues #1622, #1504): the chat transcript stops following a run the
	// instant it reads a terminal status, and `cancelled` is one, so a step
	// recorded after this line is recorded for nobody. A cancelled run's steps
	// are the record of how far it got, which is the one thing a person who
	// stopped it wants to see.
	//
	// One narrowing this does not close, and cannot: the engine stop runs
	// after the transition, in the background, so steps the sandbox produces
	// between this flush and the actual kill are invisible to the live
	// follower. They are not lost from the record, because finishVanished
	// picks them up and a reopened conversation shows them, but the person who
	// just clicked stop will not see them. That is intrinsic to cancelling,
	// and it is the one way this guarantee is weaker than the poller's.
	//
	// The read is what makes this cheap in the ordinary case: a queued task
	// has no session, so the flush returns immediately and cancelling one
	// costs a single indexed read. A failed read never blocks the
	// cancellation, and the Transition below is still the only guard on
	// double-cancelling: this is not a pre-check.
	if s.src != nil {
		if existing, gerr := s.repo.Get(ctx, tenantID, userID, id); gerr == nil {
			flushCtx, done := context.WithTimeout(ctx, cancelFlushTimeout)
			flushTaskEvents(flushCtx, s.repo, s.src, slog.Default(), existing)
			done()
		}
	}

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
	s.background(func() {
		s.stopEngineSession(stopCtx, id, sessionRef)
		// Ordered after the stop attempt, but that attempt is a no-op when
		// sessionRef is empty (a cancel that won the race against an in-flight
		// launch), so this does NOT guarantee the sandbox is down before the
		// credential dies. The comment that used to claim it did was wrong.
		//
		// The race is left alone because it resolves in the safe direction: the
		// worst outcome is a still-starting sandbox that cannot authenticate
		// and fails, which the launch path already records, against the
		// alternative of a live credential outliving the sandbox it was minted
		// for. The task is terminal either way, since the Transition above
		// already succeeded.
		revokeTaskCredential(stopCtx, s.creds, cancelled)
	})
	return cancelled, nil
}
