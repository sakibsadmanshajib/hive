package agenttask

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
func NewClient(controlPlaneURL string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(controlPlaneURL, "/"),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// Create posts a new task. POST /internal/agent-tasks/{tenant_id}/{user_id}.
func (c *Client) Create(ctx context.Context, tenantID, userID uuid.UUID, pack, instructions string) (Task, error) {
	body, err := json.Marshal(struct {
		Pack         string `json:"pack"`
		Instructions string `json:"instructions"`
	}{Pack: pack, Instructions: instructions})
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
