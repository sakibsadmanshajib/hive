package agenttask

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeEventSource struct {
	events func(ctx context.Context, ref string) ([]SandboxEvent, error)
	files  func(ctx context.Context, ref string) ([]WorkspaceFile, error)
}

func (f *fakeEventSource) Events(ctx context.Context, ref string) ([]SandboxEvent, error) {
	return f.events(ctx, ref)
}

func (f *fakeEventSource) Files(ctx context.Context, ref string) ([]WorkspaceFile, error) {
	return f.files(ctx, ref)
}

type fakeRepoForSync struct {
	listActive []Task
	get        map[uuid.UUID]Task
	appends    [][]TaskEvent
}

func (r *fakeRepoForSync) Create(context.Context, uuid.UUID, uuid.UUID, Pack, string) (Task, error) {
	panic("not used")
}
func (r *fakeRepoForSync) Get(_ context.Context, _, _ uuid.UUID, id uuid.UUID) (Task, error) {
	t, ok := r.get[id]
	if !ok {
		return Task{}, ErrNotFound
	}
	return t, nil
}
func (r *fakeRepoForSync) List(context.Context, uuid.UUID, uuid.UUID) ([]Task, error) {
	panic("not used")
}
func (r *fakeRepoForSync) Transition(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, Status, string, string, string) (Task, error) {
	panic("not used")
}
func (r *fakeRepoForSync) ListActive(context.Context) ([]Task, error) {
	return r.listActive, nil
}
func (r *fakeRepoForSync) AppendEvents(_ context.Context, _ Task, evs []TaskEvent) error {
	r.appends = append(r.appends, evs)
	return nil
}
func (r *fakeRepoForSync) ListEvents(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int64, int) ([]TaskEvent, error) {
	panic("not used")
}

func TestMapSandboxEvent(t *testing.T) {
	raw := json.RawMessage(`{"id":"x"}`)
	cases := []struct {
		name     string
		in       SandboxEvent
		wantKind TaskEventKind
		wantID   string
	}{
		{"action", SandboxEvent{ID: "a1", Kind: "ActionEvent", ToolName: "bash", ToolCallID: "c1", TextPreview: "ls"}, EventToolCall, "a1"},
		{"observation", SandboxEvent{ID: "o1", Kind: "ObservationEvent", ToolName: "bash", ToolCallID: "c1", TextPreview: "ok"}, EventToolResult, "o1"},
		{"reject", SandboxEvent{ID: "r1", Kind: "UserRejectObservation", TextPreview: "no"}, EventToolResult, "r1"},
		{"message", SandboxEvent{ID: "m1", Kind: "MessageEvent", Source: "agent", TextPreview: "hi"}, EventMessage, "m1"},
		{"error", SandboxEvent{ID: "e1", Kind: "AgentErrorEvent", TextPreview: "boom"}, EventError, "e1"},
		{"unknown falls back to status with raw payload", SandboxEvent{ID: "u1", Kind: "ConversationStateUpdateEvent", Raw: raw}, EventStatus, "u1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := mapSandboxEvent(tc.in)
			if !ok || got.Kind != tc.wantKind {
				t.Fatalf("kind = %v ok=%v, want %v", got.Kind, ok, tc.wantKind)
			}
			if got.SourceEventID != tc.wantID {
				t.Errorf("source id = %q, want %q", got.SourceEventID, tc.wantID)
			}
			if len(got.Payload) == 0 {
				t.Error("payload must never be empty")
			}
		})
	}

	t.Run("fallback stores the raw payload verbatim", func(t *testing.T) {
		got, _ := mapSandboxEvent(SandboxEvent{ID: "u2", Kind: "SomethingNew", Raw: raw})
		if !jsonEqual(got.Payload, raw) {
			t.Errorf("payload %s != raw %s", got.Payload, raw)
		}
	})
	t.Run("message role rides in payload", func(t *testing.T) {
		got, _ := mapSandboxEvent(SandboxEvent{ID: "m2", Kind: "MessageEvent", Source: "user"})
		var p map[string]string
		if err := json.Unmarshal(got.Payload, &p); err != nil || p["role"] != "user" {
			t.Errorf("role not carried: %s (%v)", got.Payload, err)
		}
	})
}

func jsonEqual(a, b json.RawMessage) bool {
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		return false
	}
	ea, _ := json.Marshal(va)
	eb, _ := json.Marshal(vb)
	return string(ea) == string(eb)
}

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 5, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"elevenchr", 10, "elevenchr"},
		{"héllo wörld", 5, "héllo"},
		{"日本語のテキストです", 4, "日本語の"},
	}
	for _, tc := range cases {
		if got := truncateRunes(tc.in, tc.n); got != tc.want {
			t.Errorf("truncateRunes(%q,%d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
	long := strings.Repeat("あ", 3000)
	if got := truncateRunes(long, maxPreviewRunes); len([]rune(got)) != maxPreviewRunes {
		t.Errorf("long multibyte truncation wrong: %d runes", len([]rune(got)))
	}
}

func TestCapEventPayload(t *testing.T) {
	small := json.RawMessage(`{"a":1}`)
	if got := capEventPayload(small); !jsonEqual(got, small) {
		t.Error("small payload must pass through unchanged")
	}
	big := json.RawMessage(`{"pad":"` + strings.Repeat("x", 100<<10) + `"}`)
	got := capEventPayload(big)
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil || m["truncated"] != true {
		t.Errorf("oversized payload not replaced by marker: %.80s", got)
	}
}

func TestFileEventID(t *testing.T) {
	mt := time.Now()
	f := WorkspaceFile{Name: "out.txt", Size: 12, ModTime: mt}
	want := "file:out.txt:12:" + strconv.FormatInt(mt.Unix(), 10)
	if got := fileEventID(f); got != want {
		t.Errorf("fileEventID = %q, want %q", got, want)
	}
	rewritten := WorkspaceFile{Name: "out.txt", Size: 20, ModTime: mt}
	if fileEventID(f) == fileEventID(rewritten) {
		t.Error("a rewritten file must get a fresh dedup id")
	}
}

func TestEventSyncerRunOnce(t *testing.T) {
	tenant := uuid.New()
	user := uuid.New()
	task := Task{
		ID: uuid.New(), TenantID: tenant, UserID: user,
		Status: StatusRunning, EngineSessionRef: "sess-1",
	}
	src := &fakeEventSource{
		events: func(context.Context, string) ([]SandboxEvent, error) {
			return []SandboxEvent{{ID: "s1", Kind: "ActionEvent", ToolName: "bash"}}, nil
		},
		files: func(context.Context, string) ([]WorkspaceFile, error) {
			return []WorkspaceFile{{Name: "a.txt", Size: 1, ModTime: time.Now()}}, nil
		},
	}
	repo := &fakeRepoForSync{listActive: []Task{task}}
	s := NewEventSyncer(repo, src, PollerConfig{})
	mustNil(t, s.RunOnce(context.Background()))
	if len(repo.appends) != 1 {
		t.Fatalf("expected 1 append batch, got %d", len(repo.appends))
	}
	kinds := []TaskEventKind{}
	for _, ev := range repo.appends[0] {
		kinds = append(kinds, ev.Kind)
	}
	want := []TaskEventKind{EventStatus, EventToolCall, EventFile}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	if repo.appends[0][1].SourceEventID != "s1" {
		t.Error("sandbox event id must be the dedup id")
	}
}

func TestEventSyncerTerminalTracking(t *testing.T) {
	tenant, user, id := uuid.New(), uuid.New(), uuid.New()
	task := Task{ID: id, TenantID: tenant, UserID: user, Status: StatusRunning, EngineSessionRef: "sess-2"}
	repo := &fakeRepoForSync{
		listActive: []Task{task},
		get: map[uuid.UUID]Task{
			id: {ID: id, TenantID: tenant, UserID: user, Status: StatusSucceeded},
		},
	}
	src := &fakeEventSource{
		events: func(context.Context, string) ([]SandboxEvent, error) { return nil, nil },
		files:  func(context.Context, string) ([]WorkspaceFile, error) { return nil, nil },
	}
	s := NewEventSyncer(repo, src, PollerConfig{})
	mustNil(t, s.RunOnce(context.Background()))

	// Second pass: the task left ListActive (poller recorded terminal).
	repo.listActive = nil
	mustNil(t, s.RunOnce(context.Background()))

	var sawTerminal bool
	for _, batch := range repo.appends {
		for _, ev := range batch {
			if ev.Kind == EventStatus && strings.Contains(string(ev.Payload), "succeeded") {
				sawTerminal = true
			}
		}
	}
	if !sawTerminal {
		t.Error("terminal status event never emitted for the vanished task")
	}
}

func mustNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
