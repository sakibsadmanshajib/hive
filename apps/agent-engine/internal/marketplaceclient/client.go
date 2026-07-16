// Package marketplaceclient fetches the tenant-enabled MCP server entries
// from the control-plane's internal marketplace endpoint (issue #309,
// agent-subsystem blueprint Step 2.3) and shapes them into the OpenHands
// SDK's native mcpServers config
// (vendor/openhands/openhands-sdk/openhands/sdk/mcp/config.py: a
// dict[str, MCPServer] under the top-level "mcpServers" key). A fetch
// failure must be treated by callers as "no MCP servers configured", never
// as a reason to fail the whole sandbox launch: the marketplace is an
// additive capability, unlike the egress allowlist (internal/egressclient)
// which fails closed.
package marketplaceclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// InternalTokenHeader mirrors apps/agent-engine/internal/egressclient's
// constant of the same name; see that package for why it is redeclared
// rather than imported (apps/agent-engine is its own Go module).
const InternalTokenHeader = "X-Internal-Token"

// Client reads tenant-enabled MCP server catalog entries from
// control-plane's GET /internal/marketplace/{tenant_id}/mcp-servers endpoint.
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

// mcpServerEntry mirrors apps/control-plane/internal/marketplace's
// mcpServerEntryWire wire shape.
type mcpServerEntry struct {
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config"`
}

type mcpServersResponse struct {
	TenantID uuid.UUID        `json:"tenant_id"`
	Servers  []mcpServerEntry `json:"servers"`
}

// Enabled resolves tenantID's enabled mcp_server catalog entries. A non-nil
// error means the catalog could not be resolved; callers should proceed
// with no MCP servers configured rather than fail the launch outright.
func (c *Client) Enabled(ctx context.Context, tenantID uuid.UUID) ([]MCPServerEntry, error) {
	url := fmt.Sprintf("%s/internal/marketplace/%s/mcp-servers", c.baseURL, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("marketplaceclient: build request: %w", err)
	}
	req.Header.Set(InternalTokenHeader, c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("marketplaceclient: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("marketplaceclient: unexpected status %d", resp.StatusCode)
	}

	var body mcpServersResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("marketplaceclient: decode response: %w", err)
	}

	out := make([]MCPServerEntry, 0, len(body.Servers))
	for _, e := range body.Servers {
		out = append(out, MCPServerEntry{Name: e.Name, Config: e.Config})
	}
	return out, nil
}
