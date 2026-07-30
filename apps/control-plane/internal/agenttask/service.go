package agenttask

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
)

// Service validates and orchestrates task lifecycle operations on top of a
// Repository and an Engine. It is the single sanctioned write path; Handler
// never talks to Repository or Engine directly.
type Service struct {
	repo   Repository
	engine Engine
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

// CreateTask persists a new task and attempts to hand it to the agent-engine.
// Any Launch error, including ErrEngineNotConfigured, transitions the task
// straight to StatusFailed so a caller never polls a task that can never
// progress: previously ErrEngineNotConfigured left the task in StatusQueued
// forever with no signal, which is exactly the silent-stuck-queue behavior
// this now closes.
func (s *Service) CreateTask(ctx context.Context, tenantID, userID uuid.UUID, pack Pack, instructions string) (Task, error) {
	if !pack.Valid() {
		return Task{}, ErrInvalidPack
	}

	t, err := s.repo.Create(ctx, tenantID, userID, pack, instructions)
	if err != nil {
		return Task{}, err
	}

	sessionRef, err := s.engine.Launch(ctx, t)
	switch {
	case err == nil:
		return s.repo.Transition(ctx, tenantID, userID, t.ID, StatusRunning, sessionRef, "", "")
	case errors.Is(err, ErrEngineNotConfigured):
		return s.repo.Transition(ctx, tenantID, userID, t.ID, StatusFailed, "", "", engineUnavailableMessage)
	default:
		slog.Default().WarnContext(ctx, "agenttask: launch failed, engine detail",
			"task_id", t.ID, "error", err)
		return s.repo.Transition(ctx, tenantID, userID, t.ID, StatusFailed, "", "", engineLaunchFailedMessage)
	}
}

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

// Cancel transitions a task to StatusCancelled. Only reachable from
// StatusQueued or StatusRunning; a task already in a terminal state returns
// ErrTerminalState. No read-then-act check here — Repository.Transition's
// UPDATE carries the "not already terminal" precondition atomically, so a
// concurrent engine callback finishing the task can never be clobbered by a
// racing Cancel (or vice versa).
func (s *Service) Cancel(ctx context.Context, tenantID, userID, id uuid.UUID) (Task, error) {
	return s.repo.Transition(ctx, tenantID, userID, id, StatusCancelled, "", "", "")
}
