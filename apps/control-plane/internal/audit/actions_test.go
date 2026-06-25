package audit_test

import (
	"encoding/json"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
	canonical "github.com/sakibsadmanshajib/hive/packages/audit-canonical"
)

// TestNewActions_SecurityTierClassification verifies that DATA_SUBJECT_REQUEST
// routes synchronously (security tier) and that all new WAL-tier actions
// correctly return false from IsSecurityAction.
func TestNewActions_SecurityTierClassification(t *testing.T) {
	tests := []struct {
		action     string
		wantSec    bool
		desc       string
	}{
		// Security tier — must be true.
		{audit.ActionDataSubjectRequest, true, "Law 25/PHIPA/OSFI B-10 data subject request"},
		// WAL tier — must be false.
		{audit.ActionLLMResponse, false, "completion metadata, WAL tier"},
		{audit.ActionRAGDocumentUpload, false, "RAG document upload, WAL tier"},
		{audit.ActionRAGDocumentDelete, false, "RAG document delete, WAL tier"},
		{audit.ActionRAGSearch, false, "RAG search query, WAL tier"},
		{audit.ActionRAGChunkRetrieved, false, "RAG chunk retrieved, WAL tier"},
		{audit.ActionFileAccess, false, "file access/download, WAL tier"},
		// Regression: existing security actions must remain true.
		{"AUTH_SIGNIN_SUCCESS", true, "existing auth action"},
		{"CROSS_TENANT_ATTEMPT", true, "existing cross-tenant action"},
		{"API_KEY_ISSUE", true, "existing key-issue action"},
		// Regression: unknown action must be false.
		{"UNKNOWN_ACTION", false, "unknown action defaults to WAL tier"},
	}

	for _, tc := range tests {
		t.Run(tc.action, func(t *testing.T) {
			got := audit.IsSecurityAction(tc.action)
			if got != tc.wantSec {
				t.Errorf("IsSecurityAction(%q) = %v, want %v (%s)",
					tc.action, got, tc.wantSec, tc.desc)
			}
		})
	}
}

// TestNewActions_ConstantValues verifies the string values of each new constant
// match the documented taxonomy so callers that hard-code these strings in SQL
// queries or dashboards are not broken by a typo.
func TestNewActions_ConstantValues(t *testing.T) {
	tests := []struct {
		constant string
		want     string
	}{
		{audit.ActionDataSubjectRequest, "DATA_SUBJECT_REQUEST"},
		{audit.ActionLLMResponse, "LLM_RESPONSE"},
		{audit.ActionRAGDocumentUpload, "RAG_DOCUMENT_UPLOAD"},
		{audit.ActionRAGDocumentDelete, "RAG_DOCUMENT_DELETE"},
		{audit.ActionRAGSearch, "RAG_SEARCH"},
		{audit.ActionRAGChunkRetrieved, "RAG_CHUNK_RETRIEVED"},
		{audit.ActionFileAccess, "FILE_ACCESS"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			if tc.constant != tc.want {
				t.Errorf("constant value mismatch: got %q want %q", tc.constant, tc.want)
			}
		})
	}
}

// TestNewActions_CanonicalSerialization verifies that each new action can be
// round-tripped through the canonical.Row path without error and that the
// resulting bytes contain the action string verbatim. This catches any
// encoding regression before emission code is wired in.
func TestNewActions_CanonicalSerialization(t *testing.T) {
	newActions := []string{
		audit.ActionDataSubjectRequest,
		audit.ActionLLMResponse,
		audit.ActionRAGDocumentUpload,
		audit.ActionRAGDocumentDelete,
		audit.ActionRAGSearch,
		audit.ActionRAGChunkRetrieved,
		audit.ActionFileAccess,
	}

	for _, action := range newActions {
		t.Run(action, func(t *testing.T) {
			row := canonical.Row{
				ActorType: "SERVICE",
				Action:    action,
				Severity:  "INFO",
				DeploySHA: "testsha",
				Env:       "test",
				TS:        "2026-06-25T00:00:00.000000Z",
			}
			got, err := row.Marshal()
			if err != nil {
				t.Fatalf("canonical.Row.Marshal() error: %v", err)
			}
			if len(got) == 0 {
				t.Fatal("Marshal returned empty bytes")
			}
			// The canonical bytes must be valid JSON.
			var parsed map[string]any
			if err := json.Unmarshal(got, &parsed); err != nil {
				t.Fatalf("Marshal produced invalid JSON: %v\nbytes: %s", err, got)
			}
			// The action field must appear verbatim.
			if parsed["action"] != action {
				t.Errorf("action field mismatch: got %q want %q", parsed["action"], action)
			}
		})
	}
}

// TestNewActions_LLMResponseAfterJSON verifies the canonical serialization of
// the LLM_RESPONSE after_json payload (completion_tokens, finish_reason,
// latency_ms) using StableJSON so key order is deterministic and safe for
// hash-chain inclusion.
func TestNewActions_LLMResponseAfterJSON(t *testing.T) {
	payload := map[string]any{
		"finish_reason":     "stop",
		"completion_tokens": 42,
		"latency_ms":        137,
	}
	stable, err := canonical.StableJSON(payload)
	if err != nil {
		t.Fatalf("StableJSON error: %v", err)
	}

	row := canonical.Row{
		ActorType: "SERVICE",
		Action:    audit.ActionLLMResponse,
		Severity:  "INFO",
		DeploySHA: "testsha",
		Env:       "test",
		TS:        "2026-06-25T00:00:00.000000Z",
		AfterJSON: stable,
	}
	got, err := row.Marshal()
	if err != nil {
		t.Fatalf("canonical.Row.Marshal() error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("Marshal produced invalid JSON: %v", err)
	}
	if parsed["action"] != audit.ActionLLMResponse {
		t.Errorf("action = %q, want %q", parsed["action"], audit.ActionLLMResponse)
	}
	// Verify after_json is present and contains expected keys.
	afterRaw, ok := parsed["after_json"]
	if !ok {
		t.Fatal("after_json missing from canonical bytes")
	}
	after, ok := afterRaw.(map[string]any)
	if !ok {
		t.Fatalf("after_json not an object: %T", afterRaw)
	}
	for _, key := range []string{"completion_tokens", "finish_reason", "latency_ms"} {
		if _, exists := after[key]; !exists {
			t.Errorf("after_json missing key %q", key)
		}
	}
}

// TestNewActions_RAGChunkRetrievedAfterJSON verifies the canonical
// serialization of the RAG_CHUNK_RETRIEVED after_json payload
// (score, document_id) per the taxonomy contract.
func TestNewActions_RAGChunkRetrievedAfterJSON(t *testing.T) {
	payload := map[string]any{
		"document_id": "11111111-1111-1111-1111-111111111111",
		"score":       0.92,
	}
	stable, err := canonical.StableJSON(payload)
	if err != nil {
		t.Fatalf("StableJSON error: %v", err)
	}

	row := canonical.Row{
		ActorType:  "SERVICE",
		Action:     audit.ActionRAGChunkRetrieved,
		ResourceID: "chunk-abc",
		Severity:   "INFO",
		DeploySHA:  "testsha",
		Env:        "test",
		TS:         "2026-06-25T00:00:00.000000Z",
		AfterJSON:  stable,
	}
	got, err := row.Marshal()
	if err != nil {
		t.Fatalf("canonical.Row.Marshal() error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("Marshal produced invalid JSON: %v", err)
	}
	if parsed["action"] != audit.ActionRAGChunkRetrieved {
		t.Errorf("action = %q, want %q", parsed["action"], audit.ActionRAGChunkRetrieved)
	}
	if parsed["resource_id"] != "chunk-abc" {
		t.Errorf("resource_id = %q, want %q", parsed["resource_id"], "chunk-abc")
	}
	afterRaw, ok := parsed["after_json"]
	if !ok {
		t.Fatal("after_json missing from canonical bytes")
	}
	after := afterRaw.(map[string]any)
	if _, exists := after["document_id"]; !exists {
		t.Error("after_json missing document_id")
	}
	if _, exists := after["score"]; !exists {
		t.Error("after_json missing score")
	}
}
