// Package agenttask owns agent task persistence for the web surface (issue
// #311, agent-subsystem blueprint Step 3.4): a task started in one web
// session must be visible and resumable from another web session, tenant-
// scoped. This package is the sync contract's server-side backing store; see
// SYNC_CONTRACT.md for the wire shapes and state machine the Wave 4 desktop
// consumer attaches to.
package agenttask

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Pack identifies which agent-engine pack (Wave 2.2) a task runs.
type Pack string

const (
	PackCoding        Pack = "coding-pack"
	PackKnowledgeWork Pack = "knowledge-work-pack"
)

// Valid reports whether p is one of the packs public.agent_tasks' CHECK
// constraint accepts.
func (p Pack) Valid() bool {
	switch p {
	case PackCoding, PackKnowledgeWork:
		return true
	default:
		return false
	}
}

// Status is a task's position in its queued -> running -> {succeeded,
// failed} state machine, with cancelled reachable from queued or running.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Task is one row of public.agent_tasks.
type Task struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	UserID           uuid.UUID
	Pack             Pack
	Instructions     string
	Status           Status
	EngineSessionRef string
	ResultSummaryRef string
	ErrorMessage     string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time
}

var (
	// ErrNotFound is returned when the requested task does not exist (or does
	// not belong to the requesting tenant/user).
	ErrNotFound = errors.New("agenttask: task not found")

	// ErrInvalidPack is returned when Pack.Valid() is false.
	ErrInvalidPack = errors.New("agenttask: pack must be coding-pack or knowledge-work-pack")

	// ErrTerminalState is returned when a caller tries to transition (e.g.
	// cancel) a task that already reached a terminal status.
	ErrTerminalState = errors.New("agenttask: task already reached a terminal state")
)
