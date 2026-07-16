// Package engineapi is the one sanctioned public door into
// apps/agent-engine/internal/engine.SandboxEngine for other modules in this
// repo (issue #305). Go's internal-package visibility rule is scoped to the
// directory tree rooted at the parent of "internal" — here, anything under
// apps/agent-engine/* — so this package (a sibling of internal/engine, not
// itself under internal/) may import it and re-export what callers outside
// apps/agent-engine need, while everything else in internal/ stays
// encapsulated. apps/control-plane/internal/agentengine is the intended
// caller: it adapts SandboxEngine to apps/control-plane/internal/agenttask's
// Engine interface.
//
// Every exported name here is a type alias or trivial wrapper — no logic
// lives in this package, only the door.
package engineapi

import "github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/engine"

type (
	// Task is engine.Task.
	Task = engine.Task
	// Status is engine.Status.
	Status = engine.Status
	// Config is engine.Config.
	Config = engine.Config
	// SandboxEngine is engine.SandboxEngine.
	SandboxEngine = engine.SandboxEngine
)

const (
	StatusQueued    = engine.StatusQueued
	StatusRunning   = engine.StatusRunning
	StatusSucceeded = engine.StatusSucceeded
	StatusFailed    = engine.StatusFailed
	StatusCancelled = engine.StatusCancelled
)

// ErrUnknownSession is engine.ErrUnknownSession.
var ErrUnknownSession = engine.ErrUnknownSession

// New constructs a SandboxEngine from cfg.
func New(cfg Config) *SandboxEngine {
	return engine.New(cfg)
}
