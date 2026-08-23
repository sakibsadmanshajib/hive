// Package agentsched serves the customer-facing scheduled-agent-task
// ("routines") CRUD surface at /v1/schedules. Persistence and the
// scheduler loop live in control-plane (apps/control-plane/internal/
// agentsched); this package is the auth boundary and wire-shape
// translator that calls into it over the internal service-to-service surface,
// mirroring apps/edge-api/internal/agenttask.
package agentsched

import (
	"errors"
	"time"
)

// Schedule mirrors control-plane's scheduleWire response shape.
type Schedule struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Instructions string     `json:"instructions"`
	Schedule     string     `json:"schedule"`
	Enabled      bool       `json:"enabled"`
	NextRunAt    *time.Time `json:"next_run_at"`
	LastRunAt    *time.Time `json:"last_run_at"`
	LastTaskID   string     `json:"last_task_id"`
	LastError    string     `json:"last_error"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

var (
	ErrNotFound      = errors.New("agentsched: schedule not found")
	ErrInvalidInput  = errors.New("agentsched: invalid name, instructions, or cadence")
	ErrRequestFailed = errors.New("agentsched: request failed")
)
