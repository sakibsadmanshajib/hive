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
	return agenttask.Status(s), resultSummary, errMessage, err
}

// Cancel interrupts sessionRef's conversation and terminates its sandbox
// process. Not called by agenttask.Service.Cancel yet (that method only
// transitions the DB row today); wiring it in is the same follow-up as
// Status above.
func (e *Engine) Cancel(ctx context.Context, sessionRef string) error {
	return e.sandbox.Cancel(ctx, sessionRef)
}
