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

// Task is one row of public.agent_tasks, plus one transient field that is
// never a column.
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

	// ProjectID is the project this run was submitted with (issue #1595), or
	// uuid.Nil for none. Written by Repository.Create and NOT read back by any
	// SELECT in this package, so a Task loaded from the database has it Nil.
	// Ownership of the project was verified in edge-api before this package was
	// reached; nothing here re-derives it, and nothing here retrieves the
	// project's passages yet (spec task 9).
	ProjectID uuid.UUID

	// BearerJWT is the task's own user's bearer JWT, set by
	// Service.CreateTask on the in-memory Task it hands to Engine.Launch and
	// never touched by Repository: it is not a column on public.agent_tasks
	// and Repository.Create/Get/List/Transition never read or write it, so a
	// Task loaded back from the database always has it empty. It exists
	// purely to reach Engine.Launch, which forwards it to the agent-engine
	// host process for a knowledge-work-pack session to later publish its
	// output as an artifact under this same tenant/user (see
	// apps/agent-engine/internal/artifactsclient's package doc for why that
	// requires the real user JWT rather than the internal service token this
	// package's own HTTP surface is otherwise guarded by).
	BearerJWT string

	// LLMAPIKey is the per-task gateway credential the sandbox authenticates
	// its model calls with, minted by Service.launch from TaskCredentials and
	// carried to the engine on the same in-memory Task that carries
	// BearerJWT. Like BearerJWT it is NOT a column on public.agent_tasks:
	// Repository never reads or writes it, so a Task loaded back from the
	// database always has it empty, and the secret exists only for as long as
	// the launch that hands it over.
	//
	// It is what makes an agent task charge the tenant that submitted it
	// (#1507). Without it the sandbox spends the launcher's one process-wide
	// key and every tenant's inference settles against a single Hive-owned
	// account, which is the defect. The credential's own id is this task's id;
	// see credentials.go.
	LLMAPIKey string
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
