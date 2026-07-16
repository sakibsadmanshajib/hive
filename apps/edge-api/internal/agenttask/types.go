// Package agenttask serves the customer-facing agent task lifecycle surface
// (issue #311, agent-subsystem blueprint Step 3.4): a task started in one web
// session is visible and resumable from another web session, tenant- and
// user-scoped. Persistence lives in control-plane
// (apps/control-plane/internal/agenttask); this package is the auth boundary
// and wire-shape translator that calls into it over the internal
// service-to-service surface. See
// apps/control-plane/internal/agenttask/SYNC_CONTRACT.md for the full
// contract.
package agenttask

import (
	"errors"
	"time"
)

// Task mirrors the control-plane's taskWire response shape.
type Task struct {
	ID               string     `json:"id"`
	Pack             string     `json:"pack"`
	Status           string     `json:"status"`
	EngineSessionRef string     `json:"engine_session_ref"`
	ResultSummaryRef string     `json:"result_summary_ref"`
	ErrorMessage     string     `json:"error_message"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	StartedAt        *time.Time `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
}

var (
	// ErrNotFound mirrors control-plane's 404: unknown task, or a task that
	// belongs to a different user.
	ErrNotFound = errors.New("agenttask: task not found")

	// ErrInvalidPack mirrors control-plane's 400 for an unrecognized pack.
	ErrInvalidPack = errors.New("agenttask: invalid pack")

	// ErrTerminalState mirrors control-plane's 409: the task already reached
	// a terminal status and cannot be cancelled again.
	ErrTerminalState = errors.New("agenttask: task already reached a terminal state")

	// ErrRequestFailed is the provider-blind catch-all for any other
	// non-2xx response or transport failure — the real cause is logged
	// server-side, never surfaced to the customer.
	ErrRequestFailed = errors.New("agenttask: request failed")
)
