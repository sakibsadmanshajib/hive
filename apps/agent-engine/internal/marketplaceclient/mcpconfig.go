package marketplaceclient

import (
	"encoding/json"
	"fmt"
)

// MCPServerEntry is one enabled catalog entry as returned by Client.Enabled:
// Name is the catalog entry's admin-curated name (becomes the mcpServers map
// key); Config is the raw, kind-specific JSON blob from
// apps/control-plane/internal/marketplace (an MCP server's command/args/env
// or url/transport fields).
type MCPServerEntry struct {
	Name   string
	Config json.RawMessage
}

// MCPServer is the subset of the OpenHands SDK's native MCPServer fields
// (vendor/openhands/openhands-sdk/openhands/sdk/mcp/config.py) an admin-
// curated baseline catalog entry needs: a stdio server (Command/Args/Env) or
// a remote server (URL/Transport). OAuth and header-auth credentials are out
// of scope for the admin-curated baseline (issue #309); a curated remote MCP
// server that needs them is a follow-up.
type MCPServer struct {
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
	Transport string            `json:"transport,omitempty"`
}

// BuildConfig decodes each entry's Config into an MCPServer and serializes
// the result as the OpenHands SDK's native {"mcpServers": {name: server}}
// JSON document (mcp.utils.to_fastmcp_mcp_config's wire shape) — the file
// the agent-engine read seam writes for a pack session to consume (issue
// #309, full wiring into a running agent-server session is a Wave 3
// follow-up; see cmd/agent-engine/main.go). An entry whose Config does not
// decode as a valid MCPServer is skipped, not fatal: one malformed catalog
// entry must not block every other enabled server from reaching the
// sandbox.
func BuildConfig(entries []MCPServerEntry) ([]byte, error) {
	servers := make(map[string]MCPServer, len(entries))
	for _, e := range entries {
		var server MCPServer
		if err := json.Unmarshal(e.Config, &server); err != nil {
			continue
		}
		servers[e.Name] = server
	}

	doc := map[string]map[string]MCPServer{"mcpServers": servers}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marketplaceclient: marshal mcp config: %w", err)
	}
	return out, nil
}
