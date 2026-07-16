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

// WaitReady blocks until socketPath is dialable (the in-SIF shim has
// created its listening socket, see apps/agent-engine/internal/sandbox's
// package doc) or ctx is done, whichever comes first. The shim's startup
// time is unpredictable but usually sub-second, so this retries on a short
// fixed interval rather than anything more elaborate.
func WaitReady(ctx context.Context, socketPath string) error {
	dialer := &net.Dialer{}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := dialer.DialContext(ctx, "unix", socketPath)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("controlclient: control socket %s never became ready: %w", socketPath, ctx.Err())
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
	InitialMessage *SendMessageRequest `json:"initial_message,omitempty"`
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
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("controlclient: decode response: %w", err)
	}
	return nil
}
