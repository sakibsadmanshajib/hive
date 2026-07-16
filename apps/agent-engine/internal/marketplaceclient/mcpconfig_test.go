package marketplaceclient_test

import (
	"encoding/json"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/marketplaceclient"
)

func TestBuildConfig(t *testing.T) {
	tests := []struct {
		name    string
		entries []marketplaceclient.MCPServerEntry
		want    string
	}{
		{
			name:    "no entries produces an empty mcpServers object",
			entries: nil,
			want:    `{"mcpServers":{}}`,
		},
		{
			name: "stdio server keeps command/args/env",
			entries: []marketplaceclient.MCPServerEntry{
				{Name: "github", Config: json.RawMessage(`{"command":"npx","args":["-y","server-github"],"env":{"GITHUB_TOKEN":"x"}}`)},
			},
			want: `{"mcpServers":{"github":{"command":"npx","args":["-y","server-github"],"env":{"GITHUB_TOKEN":"x"}}}}`,
		},
		{
			name: "remote server keeps url/transport",
			entries: []marketplaceclient.MCPServerEntry{
				{Name: "search", Config: json.RawMessage(`{"url":"https://mcp.example.invalid","transport":"http"}`)},
			},
			want: `{"mcpServers":{"search":{"url":"https://mcp.example.invalid","transport":"http"}}}`,
		},
		{
			name: "malformed entry config is skipped, not fatal",
			entries: []marketplaceclient.MCPServerEntry{
				{Name: "good", Config: json.RawMessage(`{"command":"npx"}`)},
				{Name: "bad", Config: json.RawMessage(`not json`)},
			},
			want: `{"mcpServers":{"good":{"command":"npx"}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := marketplaceclient.BuildConfig(tt.entries)
			if err != nil {
				t.Fatalf("BuildConfig: %v", err)
			}
			var gotNorm, wantNorm any
			if err := json.Unmarshal(got, &gotNorm); err != nil {
				t.Fatalf("unmarshal got: %v", err)
			}
			if err := json.Unmarshal([]byte(tt.want), &wantNorm); err != nil {
				t.Fatalf("unmarshal want: %v", err)
			}
			gotCanon, _ := json.Marshal(gotNorm)
			wantCanon, _ := json.Marshal(wantNorm)
			if string(gotCanon) != string(wantCanon) {
				t.Errorf("BuildConfig() = %s, want %s", got, tt.want)
			}
		})
	}
}
