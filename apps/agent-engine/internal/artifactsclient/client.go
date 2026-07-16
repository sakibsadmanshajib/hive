// Package artifactsclient publishes a self-contained HTML artifact (the
// output of the deck-generation and code-canvas knowledge-work-pack
// skills) to the edge-api artifacts API
// (apps/edge-api/internal/artifacts, issue #312) and returns its published
// URL.
//
// Unlike apps/agent-engine/internal/egressclient and
// .../marketplaceclient, which call control-plane's internal
// service-to-service endpoints with a shared X-Internal-Token, this client
// authenticates every call with the real per-task user's bearer JWT:
// POST /v1/artifacts is a normal tenant-scoped, JWT-gated edge-api route
// (auth.UserFrom in apps/edge-api/internal/artifacts/handler.go) with no
// internal-token bypass, and it resolves tenant_id exclusively from JWT
// claims, never from a request field. There is deliberately no
// shared-secret path here: that would let the engine forge an arbitrary
// tenant_id. The caller (the Wave 3.1/3.4 task-lifecycle code that already
// authenticated the task-start request) must supply that same JWT per
// call; this package neither caches nor mints one.
//
// This call also runs on the agent-engine host process, not from inside
// the Apptainer sandbox: the sandbox network namespace only reaches the
// egress-proxy unix socket (apps/agent-engine/internal/egressproxy), and
// routing a JWT-authenticated internal call through a tenant-configurable
// external-egress allowlist would be the wrong trust boundary for a
// same-cluster Hive-to-Hive call. The skill's artifact HTML is written to
// the mounted /workspace by the agent; the host-side engine process reads
// it back and calls Create/AddVersion once the task completes.
package artifactsclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client publishes artifacts to an edge-api instance at baseURL.
type Client struct {
	baseURL string
	http    *http.Client
}

// New constructs a Client. baseURL is edge-api's base URL (e.g.
// "http://edge-api:8080").
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Artifact mirrors apps/edge-api/internal/artifacts.VersionResponse.
type Artifact struct {
	ID           string    `json:"id"`
	Version      int       `json:"version"`
	URL          string    `json:"url"`
	VersionedURL string    `json:"versioned_url"`
	CreatedAt    time.Time `json:"created_at"`
}

// Create publishes html as a new artifact named name, authenticated as the
// tenant user identified by bearerJWT. Returns the published Artifact
// (version 1) on success.
func (c *Client) Create(ctx context.Context, bearerJWT, name, html string) (Artifact, error) {
	if strings.TrimSpace(bearerJWT) == "" {
		return Artifact{}, errors.New("artifactsclient: bearerJWT must not be blank")
	}
	if html == "" {
		return Artifact{}, errors.New("artifactsclient: html must not be empty")
	}
	body := map[string]string{"name": name, "html": html}
	return c.post(ctx, bearerJWT, "/v1/artifacts", body)
}

// AddVersion publishes html as a new version of the existing artifact
// identified by artifactID, authenticated as the tenant user identified by
// bearerJWT.
func (c *Client) AddVersion(ctx context.Context, bearerJWT, artifactID, html string) (Artifact, error) {
	if strings.TrimSpace(bearerJWT) == "" {
		return Artifact{}, errors.New("artifactsclient: bearerJWT must not be blank")
	}
	if strings.TrimSpace(artifactID) == "" {
		return Artifact{}, errors.New("artifactsclient: artifactID must not be blank")
	}
	if html == "" {
		return Artifact{}, errors.New("artifactsclient: html must not be empty")
	}
	body := map[string]string{"html": html}
	return c.post(ctx, bearerJWT, "/v1/artifacts/"+artifactID+"/versions", body)
}

func (c *Client) post(ctx context.Context, bearerJWT, path string, body map[string]string) (Artifact, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return Artifact{}, fmt.Errorf("artifactsclient: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return Artifact{}, fmt.Errorf("artifactsclient: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearerJWT)

	resp, err := c.http.Do(req)
	if err != nil {
		return Artifact{}, fmt.Errorf("artifactsclient: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return Artifact{}, fmt.Errorf("artifactsclient: unexpected status %d from %s", resp.StatusCode, path)
	}

	var out Artifact
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Artifact{}, fmt.Errorf("artifactsclient: decode response: %w", err)
	}
	return out, nil
}
