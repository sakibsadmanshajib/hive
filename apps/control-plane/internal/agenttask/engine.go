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
// returns an engine session reference to persist on the task row, and stops
// a session it launched.
//
// Cancel is part of this interface rather than an optional extra because a
// launcher that cannot be told to stop is the whole of issue #886: the slot
// a session holds is released by the launcher when the session ends, so a
// task cancelled without a stop call kept its concurrency slot until the
// sandbox finished on its own (roughly sixteen minutes on the demo box), and
// two cancels exhausted a user's ceiling. Cancel is expected to be
// idempotent-safe for an unknown or already-finished session: Service treats
// an error from it as an operator-visible warning, not a caller-visible
// failure.
type Engine interface {
	Launch(ctx context.Context, t Task) (sessionRef string, err error)
	Cancel(ctx context.Context, sessionRef string) error
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

// Cancel succeeds trivially: this engine never launched anything, so there is
// no session holding a slot and nothing for a caller to act on.
func (NotConfiguredEngine) Cancel(context.Context, string) error { return nil }
