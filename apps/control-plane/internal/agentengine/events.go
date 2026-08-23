package agentengine

// Event/files surface for both engine arms, satisfying
// agenttask.EventSource structurally the way Launch/Status/Cancel already do:
// Remote POSTs the launcher daemon's /events and /files routes; Engine
// delegates to the in-process SandboxEngine and converts types.

import (
	"context"
	"encoding/json"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/engineapi"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
)

// Events pulls one session's sandbox events through the launcher daemon.
// Deliberately NOT mapped onto agenttask.ErrEngineSessionGone on 404: that
// sentinel's semantics (fail the task now) belong to the status poller, while
// a missed events pull is retried next pass with no task-state consequence,
// so Remote.post's scoped /status|/cancel check stays untouched and this path
// answers an ordinary transient error instead.
func (r *Remote) Events(ctx context.Context, sessionRef string) ([]agenttask.SandboxEvent, error) {
	var out struct {
		Events []engineapi.Event `json:"events"`
	}
	if err := r.post(ctx, "/events", map[string]any{"session_ref": sessionRef}, &out); err != nil {
		return nil, err
	}
	events := make([]agenttask.SandboxEvent, 0, len(out.Events))
	for _, e := range out.Events {
		events = append(events, convertEvent(e))
	}
	return events, nil
}

// Files lists one session's workspace through the launcher daemon.
func (r *Remote) Files(ctx context.Context, sessionRef string) ([]agenttask.WorkspaceFile, error) {
	var out struct {
		Files []engineapi.WorkspaceFile `json:"files"`
	}
	if err := r.post(ctx, "/files", map[string]any{"session_ref": sessionRef}, &out); err != nil {
		return nil, err
	}
	files := make([]agenttask.WorkspaceFile, 0, len(out.Files))
	for _, f := range out.Files {
		files = append(files, agenttask.WorkspaceFile(f))
	}
	return files, nil
}

// Events adapts the in-process arm.
func (e *Engine) Events(ctx context.Context, sessionRef string) ([]agenttask.SandboxEvent, error) {
	events, err := e.sandbox.Events(ctx, sessionRef)
	if err != nil {
		return nil, err
	}
	out := make([]agenttask.SandboxEvent, 0, len(events))
	for _, ev := range events {
		out = append(out, convertEvent(ev))
	}
	return out, nil
}

// Files adapts the in-process arm.
func (e *Engine) Files(ctx context.Context, sessionRef string) ([]agenttask.WorkspaceFile, error) {
	files, err := e.sandbox.Files(ctx, sessionRef)
	if err != nil {
		return nil, err
	}
	out := make([]agenttask.WorkspaceFile, 0, len(files))
	for _, f := range files {
		out = append(out, agenttask.WorkspaceFile(f))
	}
	return out, nil
}

// convertEvent maps engineapi.Event onto agenttask.SandboxEvent field by
// field. The shapes are intentionally identical; the copy exists so the
// control-plane package never imports the agent-engine module's internal
// client directly.
func convertEvent(e engineapi.Event) agenttask.SandboxEvent {
	return agenttask.SandboxEvent{
		ID:          e.ID,
		Kind:        e.Kind,
		Source:      e.Source,
		Timestamp:   e.Timestamp,
		ToolName:    e.ToolName,
		ToolCallID:  e.ToolCallID,
		TextPreview: e.TextPreview,
		Raw:         json.RawMessage(e.Raw),
	}
}
