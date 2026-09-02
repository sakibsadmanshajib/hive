package agenttask

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/cpauth"
)

// Client calls control-plane's internal agent-task surface. Mirrors
// apps/edge-api/internal/rag.IngestClient's shape and error-handling
// contract (provider-blind: control-plane's raw body is never threaded into
// a returned error).
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a Client pointing at the control-plane base URL.
//
// The 15 second timeout is deliberate and is not the bound on a sandbox
// launch. Create used to block control-plane side on the launch itself (up to
// five minutes), so a cold sandbox routinely outlived this timeout and the
// browser was told 500 for a task that then ran to completion (issue #881).
// Control-plane now answers create with the persisted queued task and
// launches in the background, so every call this client makes is a short
// database operation plus, for cancel, a bounded stop call to the launcher
// (agenttask.engineCancelTimeout, 10 seconds, deliberately under this
// budget). Raising this to match a blocking server call would only have made
// an interactive request able to hang for five minutes.
func NewClient(controlPlaneURL string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(controlPlaneURL, "/"),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// Create posts a new task. POST /internal/agent-tasks/{tenant_id}/{user_id}.
// bearerJWT is the requesting user's own bearer JWT (handler.go's
// handleCreate reads it straight off the incoming request's Authorization
// header) — forwarded so a knowledge-work-pack session can later publish its
// output as an artifact under this same tenant/user. Empty when the request
// carried no recognizable Supabase JWT (e.g. API-key auth): control-plane
// and the engine both treat that as "skip publishing", never as a reason to
// fail the create.

// projectID is the project this run consults, already authorized by
// handler.go's handleCreate. uuid.Nil means no project, and travels as an
// omitted field so control-plane's decoder sees the same body it always did.
func (c *Client) Create(ctx context.Context, tenantID, userID uuid.UUID, pack, instructions string, projectID uuid.UUID, attachments []Attachment, bearerJWT string) (Task, error) {
	payload := struct {
		Pack         string `json:"pack"`
		Instructions string `json:"instructions"`
		ProjectID    string `json:"project_id,omitempty"`
		// Attachments are the person's own documents, already validated by
		// the handler (issue #1065). Control-plane carries them straight to
		// the launcher, which writes them into the sandbox working directory.
		Attachments []Attachment `json:"attachments,omitempty"`
		BearerJWT   string       `json:"bearer_jwt"`
	}{Pack: pack, Instructions: instructions, Attachments: attachments, BearerJWT: bearerJWT}
	if projectID != uuid.Nil {
		payload.ProjectID = projectID.String()
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Task{}, fmt.Errorf("agenttask.client: marshal: %w", err)
	}
	return c.do(ctx, http.MethodPost, c.basePath(tenantID, userID), bytes.NewReader(body))
}

// List returns every task for (tenantID, userID), newest first.
// GET /internal/agent-tasks/{tenant_id}/{user_id}.
func (c *Client) List(ctx context.Context, tenantID, userID uuid.UUID) ([]Task, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+c.basePath(tenantID, userID), nil)
	if err != nil {
		return nil, fmt.Errorf("agenttask.client: build request: %w", err)
	}
	cpauth.SetHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agenttask.client: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		drain(resp.Body)
		return nil, statusErr(resp.StatusCode)
	}
	var listResp struct {
		Tasks []Task `json:"tasks"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("agenttask.client: decode list response: %w", err)
	}
	return listResp.Tasks, nil
}

// Get fetches one task by id. GET /internal/agent-tasks/{tenant_id}/{user_id}/{task_id}.
func (c *Client) Get(ctx context.Context, tenantID, userID, taskID uuid.UUID) (Task, error) {
	path := c.basePath(tenantID, userID) + "/" + taskID.String()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return Task{}, fmt.Errorf("agenttask.client: build request: %w", err)
	}
	cpauth.SetHeader(req)
	return c.send(req)
}

// Cancel cancels a task. POST /internal/agent-tasks/{tenant_id}/{user_id}/{task_id}/cancel.
func (c *Client) Cancel(ctx context.Context, tenantID, userID, taskID uuid.UUID) (Task, error) {
	path := c.basePath(tenantID, userID) + "/" + taskID.String() + "/cancel"
	return c.do(ctx, http.MethodPost, path, nil)
}

// Events fetches one task's event rows after afterSeq.
// GET /internal/agent-tasks/{tenant_id}/{user_id}/{task_id}/events?after_seq=&limit=.
// The handler validates the cursor before calling; a 400 from control-plane
// still maps to ErrCursor so nothing can silently flatten it.
func (c *Client) Events(ctx context.Context, tenantID, userID, taskID uuid.UUID, afterSeq int64, limit int) ([]Event, error) {
	q := url.Values{}
	q.Set("after_seq", strconv.FormatInt(afterSeq, 10))
	q.Set("limit", strconv.Itoa(limit))
	path := c.basePath(tenantID, userID) + "/" + taskID.String() + "/events?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("agenttask.client: build request: %w", err)
	}
	cpauth.SetHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agenttask.client: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		drain(resp.Body)
		if resp.StatusCode == http.StatusBadRequest {
			return nil, ErrCursor
		}
		return nil, statusErr(resp.StatusCode)
	}
	var listResp struct {
		Events []Event `json:"events"`
	}
	// 40 MiB: the theoretical maximum one cursor page can carry (limit 500 x
	// 64 KiB payload cap plus JSON overhead). A smaller reader here turned a
	// legal maximal page into a decode error the customer saw as 500.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 40<<20)).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("agenttask.client: decode events response: %w", err)
	}
	return listResp.Events, nil
}

// Files lists one task's workspace through control-plane and the launcher.
// GET /internal/agent-tasks/{tenant_id}/{user_id}/{task_id}/files.
func (c *Client) Files(ctx context.Context, tenantID, userID, taskID uuid.UUID) ([]WorkspaceFile, error) {
	path := c.basePath(tenantID, userID) + "/" + taskID.String() + "/files"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("agenttask.client: build request: %w", err)
	}
	cpauth.SetHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agenttask.client: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		drain(resp.Body)
		return nil, statusErr(resp.StatusCode)
	}
	var listResp struct {
		Files []WorkspaceFile `json:"files"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("agenttask.client: decode files response: %w", err)
	}
	return listResp.Files, nil
}

func (c *Client) basePath(tenantID, userID uuid.UUID) string {
	return "/internal/agent-tasks/" + tenantID.String() + "/" + userID.String()
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (Task, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return Task{}, fmt.Errorf("agenttask.client: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	cpauth.SetHeader(req)
	return c.send(req)
}

func (c *Client) send(req *http.Request) (Task, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Task{}, fmt.Errorf("agenttask.client: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		drain(resp.Body)
		return Task{}, statusErr(resp.StatusCode)
	}
	var task Task
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&task); err != nil {
		return Task{}, fmt.Errorf("agenttask.client: decode response: %w", err)
	}
	return task, nil
}

// statusErr maps control-plane's status code to a sentinel error — never the
// response body, which may carry control-plane's raw failure detail
// (provider-blind boundary).
func statusErr(status int) error {
	switch status {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusBadRequest:
		return ErrInvalidPack
	case http.StatusConflict:
		return ErrTerminalState
	default:
		return ErrRequestFailed
	}
}

// drain discards the response body without threading its content anywhere.
func drain(body io.Reader) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 65536))
}
