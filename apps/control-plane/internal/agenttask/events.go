package agenttask

// Event vocabulary and shared bounds for public.agent_task_events. The six
// kinds mirror the migration's CHECK constraint exactly.

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// TaskEventKind is one of the six event kinds the CHECK constraint accepts.
type TaskEventKind string

const (
	EventStatus     TaskEventKind = "status"
	EventToolCall   TaskEventKind = "tool_call"
	EventToolResult TaskEventKind = "tool_result"
	EventMessage    TaskEventKind = "message"
	EventError      TaskEventKind = "error"
	EventFile       TaskEventKind = "file"
)

// Valid reports whether k is one of the six kinds the CHECK constraint
// accepts.
func (k TaskEventKind) Valid() bool {
	switch k {
	case EventStatus, EventToolCall, EventToolResult, EventMessage, EventError, EventFile:
		return true
	default:
		return false
	}
}

// maxPreviewRunes bounds every text preview stored on an event payload: 2000
// RUNES, not bytes, so a CJK-heavy tool output keeps most of its meaning.
const maxPreviewRunes = 2000

// maxEventPayloadBytes caps one event's marshalled payload at the same 64 KiB
// the HTTP handlers already use for request bodies: a runaway tool output
// cannot balloon a row past this.
const maxEventPayloadBytes = 64 << 10

// ErrCursor is returned when an events cursor is not a non-negative integer.
// Never silently treated as zero: acceptance requires the 400.
var ErrCursor = errors.New("agenttask: cursor must be a non-negative integer")

// TaskEvent is one row of public.agent_task_events as the read path returns
// it. Payload is whatever JSONB the writer stored; readers never parse its
// inner shape.
type TaskEvent struct {
	Seq           int64           `json:"seq"`
	SourceEventID string          `json:"source_event_id"`
	Kind          TaskEventKind   `json:"kind"`
	Payload       json.RawMessage `json:"payload"`
	CreatedAt     time.Time       `json:"created_at"`
}

// SandboxEvent is one normalized sandbox event as an EventSource hands it to
// the syncer. Kind is the SANDBOX kind name (e.g. "ActionEvent"); mapSandboxEvent
// does that translation.
type SandboxEvent struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Source      string          `json:"source"`
	Timestamp   string          `json:"timestamp"`
	ToolName    string          `json:"tool_name"`
	ToolCallID  string          `json:"tool_call_id"`
	TextPreview string          `json:"text_preview"`
	Raw         json.RawMessage `json:"raw,omitempty"`
}

// WorkspaceFile is one entry of a session workspace listing: name, size,
// mtime only. No file content ever crosses this boundary.
type WorkspaceFile struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
}

// EventSource is the narrow surface the eventsync syncer needs from whichever
// engine arm is wired. Both arms satisfy it with small adapters.
type EventSource interface {
	Events(ctx context.Context, sessionRef string) ([]SandboxEvent, error)
	Files(ctx context.Context, sessionRef string) ([]WorkspaceFile, error)
}

// truncateRunes cuts s to at most n runes without splitting one.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// capEventPayload marshals nothing: it takes an already-marshalled payload and
// guarantees it fits maxEventPayloadBytes. Oversized payloads are replaced by
// a tiny marker keeping the fact of the overflow and its size — bounded storage
// wins over preserving a runaway tool dump. Returns nil only for nil input.
func capEventPayload(payload json.RawMessage) json.RawMessage {
	if payload == nil {
		return nil
	}
	if len(payload) <= maxEventPayloadBytes {
		return payload
	}
	marker, err := json.Marshal(map[string]any{
		"truncated": true,
		"size":      len(payload),
	})
	if err != nil {
		return json.RawMessage(`{"truncated":true}`)
	}
	return marker
}
