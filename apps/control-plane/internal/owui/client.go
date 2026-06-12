// Package owui provides a minimal admin client for Open WebUI (OWUI) used by
// the control-plane to provision groups and add users to them.
package owui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config configures an OWUI admin Client. BaseURL must not end with a trailing
// slash. AdminToken is sent as a Bearer token on every request. HTTPClient is
// optional; when nil, a client with a 10s timeout is used.
type Config struct {
	BaseURL    string
	AdminToken string
	HTTPClient *http.Client
}

// Client is a thin HTTP wrapper around the OWUI admin API.
type Client struct {
	base   string
	token  string
	client *http.Client
}

// New builds a new OWUI admin Client from cfg.
func New(cfg Config) *Client {
	c := cfg.HTTPClient
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{base: cfg.BaseURL, token: cfg.AdminToken, client: c}
}

// AddUserToGroup adds a user (looked up by email) to the named OWUI group.
func (c *Client) AddUserToGroup(ctx context.Context, groupID, email string) error {
	body := map[string]string{"user_email": email}
	return c.post(ctx, fmt.Sprintf("/api/v1/groups/%s/add-user", groupID), body, nil)
}

// EnsureGroup creates a group by name. If the group already exists (409),
// it queries by name and returns the existing id. Returns the group id.
func (c *Client) EnsureGroup(ctx context.Context, name string) (string, error) {
	var created struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	err := c.post(ctx, "/api/v1/groups", map[string]string{"name": name}, &created)
	if err == nil {
		return created.ID, nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
		return c.findGroupByName(ctx, name)
	}
	return "", err
}

// SyncModelAccessControl sets the per-model access_control object in OWUI so
// that only tenants in allowedGroupIDs can read the model. The caller is
// responsible for computing the full desired allowlist (not just the delta).
//
// Passing an empty or nil allowedGroupIDs sends access_control: null, making
// the model public to all OWUI users.
//
// The function does NOT perform a prior GET to merge: the caller supplies the
// authoritative desired state and this function writes it atomically.
func (c *Client) SyncModelAccessControl(ctx context.Context, modelID string, allowedGroupIDs []string) error {
	type groupList struct {
		GroupIDs []string `json:"group_ids"`
		UserIDs  []string `json:"user_ids"`
	}
	type accessControl struct {
		Read  groupList `json:"read"`
		Write groupList `json:"write"`
	}

	var body map[string]any
	if len(allowedGroupIDs) == 0 {
		// Nil / empty → public: send access_control: null.
		body = map[string]any{
			"id":             modelID,
			"access_control": nil,
		}
	} else {
		body = map[string]any{
			"id": modelID,
			"access_control": accessControl{
				Read: groupList{
					GroupIDs: allowedGroupIDs,
					UserIDs:  []string{},
				},
				Write: groupList{
					GroupIDs: []string{},
					UserIDs:  []string{},
				},
			},
		}
	}

	if err := c.post(ctx, "/api/v1/models/", body, nil); err != nil {
		return fmt.Errorf("owui: sync model access_control %q: %w", modelID, err)
	}
	return nil
}

func (c *Client) findGroupByName(ctx context.Context, name string) (string, error) {
	var groups []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.get(ctx, "/api/v1/groups", &groups); err != nil {
		return "", err
	}
	for _, g := range groups {
		if g.Name == name {
			return g.ID, nil
		}
	}
	return "", fmt.Errorf("owui: group %q not found after 409", name)
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("owui: marshal %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("owui: %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	// Cap the response body at 1 MiB so a misbehaving (or malicious)
	// upstream OWUI cannot exhaust control-plane memory by returning an
	// unbounded payload on an admin endpoint.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return &APIError{Status: resp.StatusCode, Path: req.URL.Path, Body: string(raw)}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("owui: decode %s: %w", req.URL.Path, err)
		}
	}
	return nil
}

// APIError represents a non-2xx response from the OWUI admin API.
type APIError struct {
	Status int
	Path   string
	Body   string
}

// Error returns a short, sanitised representation of the upstream
// failure. The raw body is truncated to 200 bytes and stripped of
// newlines so that:
//   - a misbehaving OWUI cannot blow up downstream log lines;
//   - reflected request data (e.g. an Authorization header echoed back
//     on 401/403) cannot smuggle multi-line content into log/audit
//     consumers.
//
// Callers that need the full body must read APIError.Body directly.
func (e *APIError) Error() string {
	const maxBody = 200
	snippet := e.Body
	if len(snippet) > maxBody {
		snippet = snippet[:maxBody] + "..."
	}
	snippet = strings.ReplaceAll(snippet, "\n", " ")
	snippet = strings.ReplaceAll(snippet, "\r", " ")
	return fmt.Sprintf("owui: %s returned %d: %s", e.Path, e.Status, snippet)
}
