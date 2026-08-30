// Package agentengine adapts apps/agent-engine's SandboxEngine
// (apps/agent-engine/engineapi, issue #305) to
// apps/control-plane/internal/agenttask's Engine seam. This is the thin
// translation layer apps/agent-engine/engineapi's doc comment describes:
// agenttask.Task converts to engineapi.Task on the way in, engineapi's
// returned session reference goes straight back out. All the actual launch/
// control-channel/state-mapping logic lives in
// apps/agent-engine/internal/engine; nothing here duplicates it.
package agentengine

import (
	"context"
	"errors"
	"fmt"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/engineapi"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
)

// Engine implements agenttask.Engine on top of a *engineapi.SandboxEngine.
type Engine struct {
	sandbox *engineapi.SandboxEngine
}

// New constructs an Engine wrapping sandbox.
func New(sandbox *engineapi.SandboxEngine) *Engine {
	return &Engine{sandbox: sandbox}
}

// Launch adapts t and delegates to the wrapped SandboxEngine.
func (e *Engine) Launch(ctx context.Context, t agenttask.Task) (string, error) {
	return e.sandbox.Launch(ctx, engineapi.Task{
		ID:           t.ID,
		TenantID:     t.TenantID,
		UserID:       t.UserID,
		Pack:         string(t.Pack),
		Instructions: t.Instructions,
		BearerJWT:    t.BearerJWT,
		LLMAPIKey:    t.LLMAPIKey,
	})
}

// Status polls sessionRef and maps it onto agenttask.Status. Status and
// Status share identical string values (SYNC_CONTRACT.md's state machine),
// so the conversion is a plain cast. Not called by anything in this package
// yet — it is what a future background sync loop
// (SYNC_CONTRACT.md's Engine seam section) would call to advance a task
// past running.
func (e *Engine) Status(ctx context.Context, sessionRef string) (status agenttask.Status, resultSummary, errMessage string, err error) {
	s, resultSummary, errMessage, err := e.sandbox.Status(ctx, sessionRef)
	if errors.Is(err, engineapi.ErrUnknownSession) {
		// Same scoping as Remote.post's 404 mapping, so the poller can tell
		// "this session can never answer again" apart from a transient
		// failure regardless of which agenttask.Engine arm is wired in.
		// %w twice keeps errors.Is(result, engineapi.ErrUnknownSession)
		// working too, in case anything ever depends on the original chain.
		// Unlike Remote.post, this error never crosses a process boundary —
		// it only ever reaches the poller's own WARN log (see
		// agenttask.Poller.pollTask), never a customer-visible field — so
		// embedding sessionRef here is not the provider-blind leak it would
		// be on the socket arm.
		return "", "", "", fmt.Errorf("%w: %w", agenttask.ErrEngineSessionGone, err)
	}
	return agenttask.Status(s), resultSummary, errMessage, err
}

// Cancel interrupts sessionRef's conversation and terminates its sandbox
// process, which is what releases the session's concurrency slot on the
// launcher side. Called by agenttask.Service.Cancel (issue #886).
//
// Maps engineapi.ErrUnknownSession the same way Status does above, for the
// same arm-agnostic reason Remote.post's /cancel mapping exists: a session
// the engine has no memory of holds no concurrency slot either (the quota
// manager lives in the same in-memory state as the session registry), so
// Service.stopEngineSession's ErrEngineSessionGone branch (its doc comment)
// applies here too and there is nothing to warn an operator about.
func (e *Engine) Cancel(ctx context.Context, sessionRef string) error {
	err := e.sandbox.Cancel(ctx, sessionRef)
	if errors.Is(err, engineapi.ErrUnknownSession) {
		return fmt.Errorf("%w: %w", agenttask.ErrEngineSessionGone, err)
	}
	return err
}
