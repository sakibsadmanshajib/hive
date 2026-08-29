package agentengine

// Remote is the client half of the split introduced by issue #780: the
// Apptainer launcher runs as an unprivileged host process
// (apps/agent-engine/cmd/agent-engine -serve) and control-plane reaches it
// over a Unix socket bind-mounted into this container. It satisfies the same
// agenttask.Engine and agenttask.StatusChecker interfaces the in-process
// *Engine above does, so nothing downstream of buildAgentEngine changes.
//
// Why a Unix socket and not a port: no listener on any network interface, so
// the launcher is reachable only by processes the host lets open that file.
// The shared internal token is sent as well, for the same reason
// control-plane's own internal endpoints require it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
)

// internalTokenHeader mirrors apps/agent-engine/cmd/agent-engine's
// InternalTokenHeader. It is redeclared rather than imported because that
// binary is package main.
const internalTokenHeader = "X-Internal-Token"

// remoteBaseURL is a placeholder: the transport always dials the configured
// Unix socket, so this host is never resolved.
const remoteBaseURL = "http://agent-engine.internal"

// Remote talks to one agent-engine daemon.
type Remote struct {
	http  *http.Client
	token string
}

// NewRemote builds a Remote that dials socketPath for every request.
func NewRemote(socketPath, token string) *Remote {
	dialer := &net.Dialer{}
	return &Remote{
		token: token,
		http: &http.Client{
			// Generous: Launch blocks until the sandbox's control socket is
			// up and the conversation has been submitted, which includes the
			// agent-server's own start-up inside a cold container.
			Timeout: 5 * time.Minute,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return dialer.DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

func (r *Remote) Launch(ctx context.Context, t agenttask.Task) (string, error) {
	var out struct {
		SessionRef string `json:"session_ref"`
	}
	err := r.post(ctx, "/launch", map[string]any{
		"id":           t.ID,
		"tenant_id":    t.TenantID,
		"user_id":      t.UserID,
		"pack":         string(t.Pack),
		"instructions": t.Instructions,
		"bearer_jwt":   t.BearerJWT,
		// The per-task gateway credential (#1507). Travels the same Unix
		// socket the internal token already authenticates, never a network
		// interface, and is never logged on either side.
		"llm_api_key": t.LLMAPIKey,
	}, &out)
	if err != nil {
		return "", err
	}
	return out.SessionRef, nil
}

func (r *Remote) Status(ctx context.Context, sessionRef string) (agenttask.Status, string, string, error) {
	var out struct {
		Status        string `json:"status"`
		ResultSummary string `json:"result_summary"`
		ErrorMessage  string `json:"error_message"`
	}
	if err := r.post(ctx, "/status", map[string]any{"session_ref": sessionRef}, &out); err != nil {
		return "", "", "", err
	}
	return agenttask.Status(out.Status), out.ResultSummary, out.ErrorMessage, nil
}

func (r *Remote) Cancel(ctx context.Context, sessionRef string) error {
	return r.post(ctx, "/cancel", map[string]any{"session_ref": sessionRef}, nil)
}

// Health reports whether the daemon answers at all, so start-up can log a
// reachable/unreachable line instead of every task discovering it.
func (r *Remote) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteBaseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agentengine: health status %d", resp.StatusCode)
	}
	return nil
}

func (r *Remote) post(ctx context.Context, path string, body any, out any) error {
	return r.postLimit(ctx, path, body, out, 1<<20)
}

// postLimit is post with an explicit response-body cap. /events uses 8 MiB:
// one page is at most eventsPageSize events whose raw dumps the launcher caps
// at 32 KiB each, so ~3.3 MiB worst case, comfortably inside this bound —
// the old single 1 MiB reader silently truncated whole transcripts for
// sessions that grew past it.
func (r *Remote) postLarge(ctx context.Context, path string, body any, out any) error {
	return r.postLimit(ctx, path, body, out, 8<<20)
}

func (r *Remote) postLimit(ctx context.Context, path string, body any, out any, maxBytes int64) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("agentengine: encode %s request: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, remoteBaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("agentengine: build %s request: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set(internalTokenHeader, r.token)
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return fmt.Errorf("agentengine: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return fmt.Errorf("agentengine: read %s response: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		detail := e.Error
		if detail == "" {
			detail = string(raw)
		}
		// The daemon's own text names sandbox paths, egress hosts and the
		// model provider behind a failed call. That detail belongs in this
		// container's log, not in a value callers may surface, so the
		// returned error carries only the operation and the status code.
		log.Printf("agentengine: %s: status %d: %s", path, resp.StatusCode, detail)
		// The launcher maps engineapi.ErrUnknownSession onto 404
		// (apps/agent-engine/cmd/agent-engine/serve.go), which happens only
		// for /status and /cancel: the session reference is gone from its
		// in-memory registry (a launcher restart lost it) and can never
		// answer again. Scoped to those two paths deliberately: post also
		// serves /launch, and net/http.ServeMux answers 404 for ANY path it
		// has no handler for, so an unscoped check would call a routing
		// miss (e.g. control-plane and the host launcher binary drifting out
		// of sync) session-gone instead of the ordinary transient error it
		// actually is. Wrapping agenttask.ErrEngineSessionGone here, rather
		// than only in the in-process agentengine.Engine arm, is what lets
		// the poller tell this definitive case apart from a transient
		// failure regardless of which arm is wired in.
		if resp.StatusCode == http.StatusNotFound && (path == "/status" || path == "/cancel") {
			return fmt.Errorf("agentengine: %s: status %d: %w", path, resp.StatusCode, agenttask.ErrEngineSessionGone)
		}
		return fmt.Errorf("agentengine: %s: status %d", path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("agentengine: decode %s response: %w", path, err)
	}
	return nil
}

// compile-time proof the identifiers above are the ones agenttask needs.
var (
	_ agenttask.Engine        = (*Remote)(nil)
	_ agenttask.StatusChecker = (*Remote)(nil)
	_                         = uuid.Nil
)
