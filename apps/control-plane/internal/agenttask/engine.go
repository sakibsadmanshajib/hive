package agenttask

import (
	"context"
	"errors"
)

// ErrEngineNotConfigured is returned by an Engine that has no live control
// channel to apps/agent-engine yet. Service.CreateTask transitions the task
// straight to StatusFailed on this error — see NotConfiguredEngine's doc
// comment for the precise gap this signals.
var ErrEngineNotConfigured = errors.New("agenttask: engine not configured")

// Engine launches an agent-engine (Wave 2.2) session for a queued task and
// returns an engine session reference to persist on the task row.
type Engine interface {
	Launch(ctx context.Context, t Task) (sessionRef string, err error)
}

// NotConfiguredEngine is the default Engine wired until the host -> agent-
// server control channel exists. apps/agent-engine/cmd/agent-engine today is
// a CLI process launched with a bound host port for one sandbox session; the
// launch call this package needs (submit a task, get back a session
// reference, later learn success/failure) requires a second channel the
// sandbox's --network none profile currently cuts off entirely (Wave 3 gap
// tracked in the agent-subsystem blueprint's Wave 3.4 step and Wave 4's
// desktop control-channel work). Until that channel lands, every task
// created here is persisted and immediately transitioned to StatusFailed —
// the seam is wired, the far end of it is not, and the caller is told so
// rather than left polling a queued task that will never move.
type NotConfiguredEngine struct{}

func (NotConfiguredEngine) Launch(context.Context, Task) (string, error) {
	return "", ErrEngineNotConfigured
}
