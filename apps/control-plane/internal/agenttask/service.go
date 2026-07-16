package agenttask

import (
	"context"
	"errors"

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

// CreateTask persists a new task (StatusQueued) and attempts to hand it to
// the agent-engine. ErrEngineNotConfigured leaves the task queued rather than
// failed — that is a documented seam gap, not a task failure. Any other
// Launch error transitions the task straight to StatusFailed so the caller
// never has to poll a task that can never progress.
func (s *Service) CreateTask(ctx context.Context, tenantID, userID uuid.UUID, pack Pack) (Task, error) {
	if !pack.Valid() {
		return Task{}, ErrInvalidPack
	}

	t, err := s.repo.Create(ctx, tenantID, userID, pack)
	if err != nil {
		return Task{}, err
	}

	sessionRef, err := s.engine.Launch(ctx, t)
	switch {
	case err == nil:
		return s.repo.Transition(ctx, tenantID, userID, t.ID, StatusRunning, sessionRef, "", "")
	case errors.Is(err, ErrEngineNotConfigured):
		return t, nil
	default:
		return s.repo.Transition(ctx, tenantID, userID, t.ID, StatusFailed, "", "", err.Error())
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
// ErrTerminalState rather than silently no-op-ing.
func (s *Service) Cancel(ctx context.Context, tenantID, userID, id uuid.UUID) (Task, error) {
	t, err := s.repo.Get(ctx, tenantID, userID, id)
	if err != nil {
		return Task{}, err
	}
	if t.Status.terminal() {
		return Task{}, ErrTerminalState
	}
	return s.repo.Transition(ctx, tenantID, userID, id, StatusCancelled, "", "", "")
}
