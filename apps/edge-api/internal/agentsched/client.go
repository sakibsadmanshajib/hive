// Client calls control-plane's internal schedule CRUD surface. Mirrors
// apps/edge-api/internal/agenttask/client.go's shape: provider-blind error
// mapping (control-plane's raw body is never threaded into a returned error).
package agentsched

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

type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a Client pointing at the control-plane base URL. Every
// call here is a short database operation; 15s matches agenttask's client.
func NewClient(controlPlaneURL string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(controlPlaneURL, "/"),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) basePath(tenantID, userID uuid.UUID) string {
	return "/internal/agent-schedules/" + tenantID.String() + "/" + userID.String()
}

type createBody struct {
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
	Schedule     string `json:"schedule"`
}

type updateBody struct {
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
	Schedule     string `json:"schedule"`
	Enabled      bool   `json:"enabled"`
}

// Create posts a new schedule.
func (c *Client) Create(ctx context.Context, tenantID, userID uuid.UUID, name, instructions, schedule string) (Schedule, error) {
	body, err := json.Marshal(createBody{Name: name, Instructions: instructions, Schedule: schedule})
	if err != nil {
		return Schedule{}, fmt.Errorf("agentsched.client: marshal: %w", err)
	}
	return c.send(ctx, http.MethodPost, c.basePath(tenantID, userID), body)
}

// List returns every schedule for (tenantID, userID), newest first.
func (c *Client) List(ctx context.Context, tenantID, userID uuid.UUID) ([]Schedule, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+c.basePath(tenantID, userID), nil)
	if err != nil {
		return nil, fmt.Errorf("agentsched.client: build request: %w", err)
	}
	cpauth.SetHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agentsched.client: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		drain(resp.Body)
		return nil, statusErr(resp.StatusCode)
	}
	var listResp struct {
		Schedules []Schedule `json:"schedules"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("agentsched.client: decode list response: %w", err)
	}
	return listResp.Schedules, nil
}

// Get fetches one schedule by id.
func (c *Client) Get(ctx context.Context, tenantID, userID, id uuid.UUID) (Schedule, error) {
	var out Schedule
	err := c.exchange(ctx, http.MethodGet, c.basePath(tenantID, userID)+"/"+id.String(), nil, &out)
	return out, err
}

// Update replaces one schedule's mutable fields.
func (c *Client) Update(ctx context.Context, tenantID, userID, id uuid.UUID, b updateBody) (Schedule, error) {
	body, err := json.Marshal(b)
	if err != nil {
		return Schedule{}, fmt.Errorf("agentsched.client: marshal: %w", err)
	}
	var out Schedule
	err = c.exchange(ctx, http.MethodPut, c.basePath(tenantID, userID)+"/"+id.String(), body, &out)
	return out, err
}

// Delete removes one schedule.
func (c *Client) Delete(ctx context.Context, tenantID, userID, id uuid.UUID) error {
	return c.exchange(ctx, http.MethodDelete, c.basePath(tenantID, userID)+"/"+id.String(), nil, nil)
}

// send is the POST create helper: builds the request, sets the internal token
// header, decodes the created Schedule.
func (c *Client) send(ctx context.Context, method, path string, body []byte) (Schedule, error) {
	var out Schedule
	err := c.exchange(ctx, method, path, body, &out)
	return out, err
}

// exchange runs one HTTP exchange against control-plane and, when out is
// non-nil, decodes the JSON response into it. 204 carries no body by design;
// decoding is skipped for it.
func (c *Client) exchange(ctx context.Context, method, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("agentsched.client: build request: %w", err)
	}
	if body != nil {
		req = req.Clone(ctx)
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Type", "application/json")
	}
	cpauth.SetHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("agentsched.client: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		drain(resp.Body)
		return statusErr(resp.StatusCode)
	}
	if out == nil {
		drain(resp.Body)
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("agentsched.client: decode response: %w", err)
	}
	return nil
}

// statusErr maps control-plane's status code to a sentinel error, never the
// response body (provider-blind boundary).
func statusErr(status int) error {
	switch status {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusBadRequest:
		return ErrInvalidInput
	default:
		return ErrRequestFailed
	}
}

// drain discards the response body without threading its content anywhere.
func drain(body io.Reader) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 65536))
}
