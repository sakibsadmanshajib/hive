// Package egressclient fetches the effective egress-policy allowlist for a
// tenant/user pair from the control-plane's internal service-to-service
// endpoint (apps/control-plane/internal/egress, issue #308). A fetch failure
// must be treated by callers as "no hosts allowed" (fail closed), never as
// unrestricted egress.
package egressclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// InternalTokenHeader mirrors
// apps/control-plane/internal/platform/http.InternalTokenHeader. It is
// duplicated rather than imported: Go internal-package visibility does not
// cross module boundaries (apps/agent-engine is its own module with its own
// go.mod), so apps/edge-api/internal/cpauth is equally unreachable from
// here for the same reason — edge-api independently redeclares this same
// constant rather than importing across the boundary.
const InternalTokenHeader = "X-Internal-Token"

// Client reads the effective egress policy from control-plane's
// GET /internal/egress-policy/{tenant_id}/{user_id} endpoint.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New constructs a Client. baseURL is the control-plane base URL (e.g.
// "http://control-plane:8081"); token is the shared internal-service secret
// sent as InternalTokenHeader.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    http.DefaultClient,
	}
}

type effectiveResponse struct {
	TenantID     uuid.UUID `json:"tenant_id"`
	UserID       uuid.UUID `json:"user_id"`
	AllowedHosts []string  `json:"allowed_hosts"`
}

// Effective resolves the allowed-hosts list that applies to tenantID+userID.
// userID may be uuid.Nil to resolve the tenant-wide default. A non-nil error
// means the policy could not be resolved; callers must treat this as zero
// allowed hosts, never as unrestricted egress.
func (c *Client) Effective(ctx context.Context, tenantID, userID uuid.UUID) ([]string, error) {
	url := fmt.Sprintf("%s/internal/egress-policy/%s/%s", c.baseURL, tenantID, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("egressclient: build request: %w", err)
	}
	req.Header.Set(InternalTokenHeader, c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("egressclient: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("egressclient: unexpected status %d", resp.StatusCode)
	}

	var body effectiveResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("egressclient: decode response: %w", err)
	}
	return body.AllowedHosts, nil
}
