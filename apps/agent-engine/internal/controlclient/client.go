// Package controlclient is the host-side HTTP client for the OpenHands
// agent-server's REST API (vendor/openhands/openhands-agent-server), reached
// over the Unix socket apps/agent-engine/internal/sandbox's control channel
// bind-mounts into the sandbox (issue #305). Every route below is verified
// against the vendored FastAPI router source, not guessed:
//
//   - POST /api/conversations                          - conversation_router.start_conversation
//   - POST /api/conversations/{id}/run                  - conversation_router.run_conversation
//   - GET  /api/conversations/{id}                       - conversation_router.get_conversation
//   - POST /api/conversations/{id}/interrupt             - conversation_router.interrupt_conversation
//   - GET  /api/conversations/{id}/agent_final_response  - conversation_router.get_conversation_agent_final_response
//   - DELETE /api/conversations/{id}                     - conversation_router.delete_conversation
//
// (vendor/openhands/openhands-agent-server/openhands/agent_server/conversation_router.py;
// api.py mounts conversation_router under the "/api" prefix behind the
// check_session_api_key + require_initialized dependencies).
//
// Auth: the agent-server accepts an optional X-Session-API-Key header
// (openhands/agent_server/dependencies.py: _SESSION_API_KEY_HEADER). When
// the server has no configured session_api_keys the check passes regardless
// of the header (openhands/agent_server/config.py); this client sends the
// header only when SessionAPIKey is non-empty.
package controlclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// SessionAPIKeyHeader mirrors vendor/openhands/openhands-agent-server's
// openhands/agent_server/dependencies.py _SESSION_API_KEY_HEADER.
const SessionAPIKeyHeader = "X-Session-API-Key"

// controlBaseURL is a placeholder base URL: Client's http.Transport always
// dials the configured Unix socket regardless of host, so the scheme/host
// here are never actually resolved over the network.
const controlBaseURL = "http://agent-server.control"

// maxSuccessBodyBytes caps a 2xx response body this client decodes.
const maxSuccessBodyBytes = 10 << 20 // 10 MiB

// Client talks to one agent-server instance over a Unix socket.
type Client struct {
	http          *http.Client
	sessionAPIKey string
}

// New builds a Client that dials socketPath for every request. sessionAPIKey
// may be empty (no auth enforced server-side, e.g. local/dev).
func New(socketPath string, sessionAPIKey string) *Client {
	dialer := &net.Dialer{}
	return &Client{
		sessionAPIKey: sessionAPIKey,
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return dialer.DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

// WaitReady blocks until the agent-server behind socketPath answers an HTTP
// request, or ctx is done, whichever comes first.
//
// Dialability alone is NOT readiness, and treating it as such is what made
// the first real launch on the demo box fail. The socat shim inside the SIF
// creates its listening socket immediately (it is the first line of the
// image's runscript), while the Python agent-server it forwards to takes
// tens of seconds to import its dependencies and bind its port. In that
// window the socket connects and the shim's forward attempt fails, so the
// very next request dies with a bare EOF. Requiring a real HTTP response
// closes that window: any status code proves the far end is a live HTTP
// server, and only a transport failure counts as not-yet-ready.
func WaitReady(ctx context.Context, socketPath string) error {
	client := New(socketPath, "")
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, controlBaseURL+"/health", nil)
		if err != nil {
			cancel()
			return fmt.Errorf("controlclient: build readiness probe: %w", err)
		}
		resp, err := client.http.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
			_ = resp.Body.Close()
			cancel()
			return nil
		}
		lastErr = err
		cancel()
		select {
		case <-ctx.Done():
			return fmt.Errorf("controlclient: agent-server on %s never became ready (last error: %v): %w", socketPath, lastErr, ctx.Err())
		case <-ticker.C:
		}
	}
}

// ExecutionStatus mirrors
// vendor/openhands/openhands-sdk/openhands/sdk/conversation/state.py's
// ConversationExecutionStatus enum values.
type ExecutionStatus string

const (
	StatusIdle                   ExecutionStatus = "idle"
	StatusRunning                ExecutionStatus = "running"
	StatusPaused                 ExecutionStatus = "paused"
	StatusWaitingForConfirmation ExecutionStatus = "waiting_for_confirmation"
	StatusFinished               ExecutionStatus = "finished"
	StatusErrored                ExecutionStatus = "error"
	StatusStuck                  ExecutionStatus = "stuck"
	StatusDeleting               ExecutionStatus = "deleting"
)

// Workspace is the wire shape of
// vendor/openhands/openhands-sdk/openhands/sdk/workspace/local.py's
// LocalWorkspace: {"kind": "LocalWorkspace", "working_dir": "..."}. kind is
// a Pydantic DiscriminatedUnionMixin computed field equal to the class name
// (openhands/sdk/utils/models.py DiscriminatedUnionMixin.kind).
type Workspace struct {
	Kind       string `json:"kind"`
	WorkingDir string `json:"working_dir"`
}

// LocalWorkspace builds a Workspace pointing at workingDir (the sandbox's
// fixed /workspace bind mount, see apps/agent-engine/internal/sandbox).
func LocalWorkspace(workingDir string) Workspace {
	return Workspace{Kind: "LocalWorkspace", WorkingDir: workingDir}
}

// TextContent mirrors
// vendor/openhands/openhands-sdk/openhands/sdk/llm/message.py's TextContent.
type TextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Text builds a TextContent from plain text.
func Text(text string) TextContent {
	return TextContent{Type: "text", Text: text}
}

// SendMessageRequest mirrors
// vendor/openhands/openhands-sdk/openhands/sdk/conversation/request.py's
// SendMessageRequest.
type SendMessageRequest struct {
	Role    string        `json:"role"`
	Content []TextContent `json:"content"`
	Run     bool          `json:"run"`
}

// StartConversationRequest is the subset of
// vendor/openhands/openhands-sdk/openhands/sdk/conversation/request.py's
// StartConversationRequest fields this client populates.
//
// AgentProfileID selects a server-side-resolved agent profile (LLM + tools
// + confirmation policy); building a full inline `agent` payload here would
// require plumbing LLM credentials through this client, which is out of
// scope — issue #311's agenttask.Task carries no prompt or LLM/profile
// reference yet (SYNC_CONTRACT.md), so callers of this package must resolve
// AgentProfileID some other way until that lands. See the engine package's
// doc comment for this known gap.
type StartConversationRequest struct {
	Workspace      Workspace           `json:"workspace"`
	AgentProfileID *uuid.UUID          `json:"agent_profile_id,omitempty"`
	AgentSettings  *AgentSettings      `json:"agent_settings,omitempty"`
	InitialMessage *SendMessageRequest `json:"initial_message,omitempty"`
}

// AgentSettings is the inline alternative to AgentProfileID: the subset of
// vendor/openhands/openhands-sdk's OpenHandsAgentSettings
// (openhands/sdk/settings/model.py) needed to launch a conversation without
// a profile stored server-side. StartConversationRequest's own validator
// (openhands/sdk/conversation/request.py) treats agent_profile_id and
// agent_settings as mutually exclusive, and requires one of them.
//
// This exists because a sandbox launched with --containall has no persisted
// profile store at all: every session starts from a fresh, empty container
// filesystem, so an agent_profile_id can only ever resolve to
// ProfileNotFound there. The credentials travel over the per-session Unix
// control socket, never over a network, and are never written to disk by
// this client.
type AgentSettings struct {
	AgentKind string      `json:"agent_kind"`
	LLM       LLMSettings `json:"llm"`

	// Tools optionally names the tool sets the agent should launch with.
	// Omitted (nil), the sandbox's OpenHandsAgentSettings default applies:
	// the exec set (terminal, file_editor, task_tracker), no browser. A
	// non-empty list is used by create_agent exactly as given, so a launch
	// that wants the browser must repeat the exec names too
	// (openhands/sdk/tool/defaults.py DEFAULT_EXEC_TOOL_NAMES +
	// BROWSER_TOOL_NAME). The serving layer still has the last word: on a
	// runtime where chromium is unusable, conversation_service strips an
	// unusable browser_tool_set spec instead of letting tool resolution die
	// at init, degrading to the exec-only set exactly as its profile-path
	// injection does. Existing tasks that leave this field unset are
	// unaffected either way.
	Tools []ToolSpec `json:"tools,omitempty"`

	// AgentContext optionally shapes the agent's system prompt. Omitted
	// (nil), OpenHandsAgentSettings.agent_context falls back to its own
	// default_factory=AgentContext (openhands/sdk/settings/model.py), which
	// is byte for byte the agent every Hive launch produced before this
	// field existed. Nothing else here reaches the system prompt: the pack's
	// AGENTS.md is a bind mount the agent reads, and the task instructions
	// are the conversation's first user message.
	AgentContext *AgentContext `json:"agent_context,omitempty"`
}

// AgentContext is the subset of
// vendor/openhands/openhands-sdk/openhands/sdk/context/agent_context.py's
// AgentContext that Hive populates. Every other field on that model has a
// default, so a body carrying only the fields below builds the same object
// the SDK's own default_factory would, plus the suffix.
//
// Only the system-prompt suffix is plumbed. AgentContext also carries a
// `skills` list, and that one is NOT usable from here as things stand: the
// sandbox launches with --containall, so the SDK's own skill loader reads an
// empty home directory every session, and a real per-tenant skills story needs
// either this list populated from a store Hive does not have yet or a second
// read-only bind mount alongside the pack. Adding the field without that
// decision would ship a knob that resolves to nothing.
type AgentContext struct {
	// SystemMessageSuffix is appended to the agent's rendered system prompt
	// (AgentContext.get_system_message_suffix, consumed by the agent's
	// system-prompt assembly). Empty is omitted rather than sent as "", so an
	// unconfigured deployment produces no such key at all.
	SystemMessageSuffix string `json:"system_message_suffix,omitempty"`
}

// ToolSpec is one entry of AgentSettings.Tools: a tool-set name from the
// sandbox's tool registry (openhands/sdk/tool/registry.py), resolved to an
// implementation only at agent init.
type ToolSpec struct {
	Name string `json:"name"`
}

// DefaultExecToolNames mirrors openhands/sdk/tool/defaults.py's
// DEFAULT_EXEC_TOOL_NAMES. Any explicit Tools list must carry these, since a
// non-empty list replaces the default set wholesale.
var DefaultExecToolNames = []string{"terminal", "file_editor", "task_tracker"}

// BrowserToolName mirrors openhands/sdk/tool/defaults.py's BROWSER_TOOL_NAME.
const BrowserToolName = "browser_tool_set"

// LLMSettings mirrors the LLM fields (openhands/sdk/llm/llm.py) an
// OpenAI-compatible gateway needs. Every other field on that model has a
// default, so this is the whole shape a Hive-gateway-backed launch sets.
type LLMSettings struct {
	Model   string `json:"model"`
	BaseURL string `json:"base_url,omitempty"`
	APIKey  string `json:"api_key,omitempty"`
	UsageID string `json:"usage_id,omitempty"`

	// Stream asks the agent-server to publish token-level deltas while the
	// model is still producing them. It gates real behaviour rather than
	// merely describing it: agent_server/event_service.py computes
	// streaming_enabled as "the agent is an ACPAgent, or any of its LLMs has
	// stream set", and only then wires the token callback that publishes
	// StreamingDeltaEvent to subscribers. openhands/sdk/llm/llm.py defaults
	// this field to False, so a payload that omits it launches a sandbox that
	// can never emit a delta.
	//
	// No omitempty on purpose: the false case is a meaningful statement about
	// a launch, and a launch payload that silently drops the field is exactly
	// how this stayed off unnoticed. Deltas are transient by design (they
	// bypass the callback chain that persists ConversationState.events), so
	// this buys live delivery only, never replay.
	Stream bool `json:"stream"`
}

// ConversationInfo is the subset of
// openhands/agent_server/models.py's ConversationInfo response fields this
// client reads.
type ConversationInfo struct {
	ID              uuid.UUID       `json:"id"`
	ExecutionStatus ExecutionStatus `json:"execution_status"`
}

type finalResponseBody struct {
	Response string `json:"response"`
}

// StatusError is returned when the agent-server responds with an HTTP
// status this client did not treat as success. Callers that need to
// special-case a status (e.g. 409 on an already-running conversation) can
// errors.As into this type.
type StatusError struct {
	StatusCode int
	Detail     string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("controlclient: unexpected status %d: %s", e.StatusCode, e.Detail)
}

// StartConversation calls POST /api/conversations
// (conversation_router.start_conversation), creating a new conversation in
// the idle state. It does not start the agent loop; call Run afterward.
func (c *Client) StartConversation(ctx context.Context, req StartConversationRequest) (ConversationInfo, error) {
	var info ConversationInfo
	if err := c.doJSON(ctx, http.MethodPost, "/api/conversations", req, &info); err != nil {
		return ConversationInfo{}, err
	}
	return info, nil
}

// Run calls POST /api/conversations/{id}/run
// (conversation_router.run_conversation), starting the agent loop in the
// background. A 409 (conversation already running) is treated as success:
// idempotent from this client's point of view.
func (c *Client) Run(ctx context.Context, conversationID uuid.UUID) error {
	path := fmt.Sprintf("/api/conversations/%s/run", conversationID)
	err := c.doJSON(ctx, http.MethodPost, path, nil, nil)
	var statusErr *StatusError
	if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusConflict {
		return nil
	}
	return err
}

// GetConversation calls GET /api/conversations/{id}
// (conversation_router.get_conversation) — the poll path for the
// conversation's current execution_status.
func (c *Client) GetConversation(ctx context.Context, conversationID uuid.UUID) (ConversationInfo, error) {
	var info ConversationInfo
	path := fmt.Sprintf("/api/conversations/%s", conversationID)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &info); err != nil {
		return ConversationInfo{}, err
	}
	return info, nil
}

// Interrupt calls POST /api/conversations/{id}/interrupt
// (conversation_router.interrupt_conversation): cancels the in-flight
// request immediately rather than waiting for it to finish (unlike pause),
// transitioning the conversation to paused.
func (c *Client) Interrupt(ctx context.Context, conversationID uuid.UUID) error {
	path := fmt.Sprintf("/api/conversations/%s/interrupt", conversationID)
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

// FinalResponse calls GET /api/conversations/{id}/agent_final_response
// (conversation_router.get_conversation_agent_final_response): the agent's
// last finish/text message, empty if none yet.
func (c *Client) FinalResponse(ctx context.Context, conversationID uuid.UUID) (string, error) {
	var body finalResponseBody
	path := fmt.Sprintf("/api/conversations/%s/agent_final_response", conversationID)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &body); err != nil {
		return "", err
	}
	return body.Response, nil
}

// Delete calls DELETE /api/conversations/{id}
// (conversation_router.delete_conversation): permanently removes the
// conversation, freeing its resources inside the sandbox.
func (c *Client) Delete(ctx context.Context, conversationID uuid.UUID) error {
	path := fmt.Sprintf("/api/conversations/%s", conversationID)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

// doJSON issues one request. body is JSON-encoded when non-nil; out is
// JSON-decoded from the response body when non-nil. Any non-2xx status
// becomes a *StatusError.
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("controlclient: encode request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, controlBaseURL+path, reader)
	if err != nil {
		return fmt.Errorf("controlclient: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.sessionAPIKey != "" {
		req.Header.Set(SessionAPIKeyHeader, c.sessionAPIKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("controlclient: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &StatusError{StatusCode: resp.StatusCode, Detail: string(detail)}
	}
	if out == nil {
		return nil
	}
	// maxSuccessBodyBytes: a conversation's own state is small JSON; capped
	// the same way the error path already is so a misbehaving agent-server
	// can't make this client buffer an unbounded response.
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSuccessBodyBytes)).Decode(out); err != nil {
		return fmt.Errorf("controlclient: decode response: %w", err)
	}
	return nil
}
