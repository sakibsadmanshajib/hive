package controlclient_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/controlclient"
)

func loadFixture(t *testing.T) []json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "events_search.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var pages []struct {
		Items      []json.RawMessage `json:"items"`
		NextPageID string            `json:"next_page_id"`
	}
	if err := json.Unmarshal(raw, &pages); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	out := make([]json.RawMessage, len(pages))
	for i := range pages {
		enc, err := json.Marshal(pages[i])
		if err != nil {
			t.Fatalf("re-encode page %d: %v", i, err)
		}
		out[i] = enc
	}
	return out
}

func newFakeEventsServer(t *testing.T, pages []json.RawMessage) string {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "agent.sock")
	l, err := net.Listen("unix", socketPath)
	mustNoErr(t, err)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(200)
			return
		}
		idx := 0
		pid := r.URL.Query().Get("page_id")
		if pid != "" {
			idx = 1
		}
		if idx >= len(pages) {
			http.Error(w, "no such page", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(pages[idx])
	}))
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return socketPath
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearchEventsFixture(t *testing.T) {
	pages := loadFixture(t)
	socketPath := newFakeEventsServer(t, pages)
	client := controlclient.New(socketPath, "")

	convoID := uuid.New()
	events, err := client.SearchEvents(context.Background(), convoID)
	mustNoErr(t, err)

	if len(events) != 6 {
		t.Fatalf("expected 6 events across 2 pages, got %d", len(events))
	}

	cases := []struct {
		id, kind, toolName, toolCallID string
		previewContains                string
	}{
		{"evt-0001", "MessageEvent", "", "", "Fix the failing test"},
		{"evt-0002", "ActionEvent", "execute_bash", "call_abc", "run the tests"},
		{"evt-0003", "ObservationEvent", "execute_bash", "call_abc", "1 failed"},
		{"evt-0004", "ConversationStateUpdateEvent", "", "", ""},
		{"evt-0005", "AgentErrorEvent", "", "", "LLM request failed"},
		{"evt-0006", "MessageEvent", "", "", "Fixed it."},
	}
	for i, tc := range cases {
		got := events[i]
		if got.ID != tc.id || got.Kind != tc.kind || got.ToolName != tc.toolName || got.ToolCallID != tc.toolCallID {
			t.Errorf("event %d = %+v, want id=%s kind=%s tool=%s call=%s",
				i, got, tc.id, tc.kind, tc.toolName, tc.toolCallID)
		}
		if tc.previewContains != "" && !strings.Contains(got.TextPreview, tc.previewContains) {
			t.Errorf("event %s preview %q missing %q", tc.id, got.TextPreview, tc.previewContains)
		}
		if got.Raw == nil {
			t.Errorf("event %s lost its raw payload", tc.id)
		}
	}
}
