// Package agentsched owns scheduled agent tasks ("routines"): a user-defined
// recurring prompt the Scheduler turns into a real agent task on its cadence,
// through the same Service.CreateTask path a manual creation uses, so a
// scheduled run is engine-gated and sandbox-quota-gated exactly like a manual
// one. It is NOT metered by that path, and the earlier wording here claiming
// otherwise was the twin of the false comment on TaskCreator: credits are
// charged per model turn where those turns are dispatched, and solvency is a
// check this package makes explicitly, at creation and again at every launch
// (see Solvency, issue #1490). First slice carries fixed cadences only
// (daily, weekly, interval:N hours); no cron-expression UX.
package agentsched

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Schedule is one row of public.agent_task_schedules.
type Schedule struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	UserID       uuid.UUID
	Name         string
	Instructions string
	Schedule     string // "daily", "weekly", or "interval:N" (N hours, 1..168)
	Enabled      bool
	NextRunAt    *time.Time
	LastRunAt    *time.Time
	LastTaskID   *uuid.UUID
	LastError    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

var (
	// ErrNotFound is returned when the requested schedule does not exist, or
	// does not belong to the requesting tenant/user.
	ErrNotFound = errors.New("agentsched: schedule not found")

	// ErrInvalidName is returned when Name is empty or over 100 characters.
	ErrInvalidName = errors.New("agentsched: name must be 1-100 characters")

	// ErrInvalidInstructions is returned when Instructions is empty or over
	// 4000 characters after sanitization.
	ErrInvalidInstructions = errors.New("agentsched: instructions must be 1-4000 characters")

	// ErrInvalidSchedule is returned when Schedule does not match the
	// restricted first-slice grammar.
	ErrInvalidSchedule = errors.New("agentsched: schedule must be daily, weekly, or interval:N with N between 1 and 168")
)

const (
	maxNameLength         = 100
	maxInstructionsLength = 4000
)
