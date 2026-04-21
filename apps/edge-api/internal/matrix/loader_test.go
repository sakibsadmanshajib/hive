package matrix

import (
	"testing"
)

const testMatrixJSON = `{
  "version": "0.1.0",
  "generated": "2026-03-28",
  "endpoints": [
    {"method": "GET", "path": "/v1/models", "status": "supported_now", "phase": 1, "notes": "Lists available models"},
    {"method": "POST", "path": "/v1/chat/completions", "status": "planned_for_launch", "phase": 6, "notes": "Chat completion"},
    {"method": "GET", "path": "/v1/files/{file_id}", "status": "supported_now", "phase": 10, "notes": "Retrieve file metadata"},
    {"method": "GET", "path": "/v1/files/{file_id}/content", "status": "supported_now", "phase": 10, "notes": "Retrieve file content"},
    {"method": "GET", "path": "/v1/batches/{batch_id}", "status": "supported_now", "phase": 10, "notes": "Retrieve batch"},
    {"method": "GET", "path": "/v1/assistants", "status": "explicitly_unsupported_at_launch", "phase": null, "notes": "Assistants"},
    {"method": "GET", "path": "/v1/organization/users", "status": "out_of_scope", "phase": null, "notes": "Org admin"},
    {"method": "POST", "path": "/v1/models", "status": "explicitly_unsupported_at_launch", "phase": null, "notes": "Not a real endpoint"}
  ]
}`

func TestLoadMatrixFromBytes(t *testing.T) {
	m, err := LoadMatrixFromBytes([]byte(testMatrixJSON))
	if err != nil {
		t.Fatalf("LoadMatrixFromBytes failed: %v", err)
	}

	if len(m.Endpoints) != 8 {
		t.Errorf("endpoint count = %d, want 8", len(m.Endpoints))
	}

	if m.Version != "0.1.0" {
		t.Errorf("version = %q, want %q", m.Version, "0.1.0")
	}
}

func TestLookup(t *testing.T) {
	m, err := LoadMatrixFromBytes([]byte(testMatrixJSON))
	if err != nil {
		t.Fatalf("LoadMatrixFromBytes failed: %v", err)
	}

	tests := []struct {
		name   string
		method string
		path   string
		want   EndpointStatus
	}{
		{
			name:   "GET /v1/models is supported_now",
			method: "GET",
			path:   "/v1/models",
			want:   StatusSupportedNow,
		},
		{
			name:   "POST /v1/chat/completions is planned_for_launch",
			method: "POST",
			path:   "/v1/chat/completions",
			want:   StatusPlannedForLaunch,
		},
		{
			name:   "GET /v1/assistants is explicitly_unsupported_at_launch",
			method: "GET",
			path:   "/v1/assistants",
			want:   StatusExplicitlyUnsupported,
		},
		{
			name:   "GET /v1/organization/users is out_of_scope",
			method: "GET",
			path:   "/v1/organization/users",
			want:   StatusOutOfScope,
		},
		{
			name:   "GET /v1/unknown returns unknown",
			method: "GET",
			path:   "/v1/unknown",
			want:   StatusUnknown,
		},
		{
			name:   "POST /v1/models distinguished from GET /v1/models",
			method: "POST",
			path:   "/v1/models",
			want:   StatusExplicitlyUnsupported,
		},
		{
			name:   "GET /v1/chat/completions not same as POST",
			method: "GET",
			path:   "/v1/chat/completions",
			want:   StatusUnknown,
		},
		{
			name:   "GET concrete file metadata path matches templated route",
			method: "GET",
			path:   "/v1/files/file-abc",
			want:   StatusSupportedNow,
		},
		{
			name:   "GET concrete file content path matches templated route",
			method: "GET",
			path:   "/v1/files/file-abc/content",
			want:   StatusSupportedNow,
		},
		{
			name:   "GET concrete batch path matches templated route",
			method: "GET",
			path:   "/v1/batches/batch-123",
			want:   StatusSupportedNow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.Lookup(tt.method, tt.path)
			if got != tt.want {
				t.Errorf("Lookup(%q, %q) = %q, want %q", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

func TestLoadMatrixInvalidJSON(t *testing.T) {
	_, err := LoadMatrixFromBytes([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}
