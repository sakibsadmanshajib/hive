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

func (r *fakeRepoForSync) Create(context.Context, uuid.UUID, uuid.UUID, Pack, string, uuid.UUID) (Task, error) {
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
		{"unknown falls back to status with raw payload", SandboxEvent{ID: "u1", Kind: "SomeFutureOpenHandsEvent", Raw: raw}, EventStatus, "u1"},
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
		got, ok := mapSandboxEvent(SandboxEvent{ID: "m2", Kind: "MessageEvent", Source: "assistant"})
		if !ok {
			t.Fatal("assistant-role message must not be filtered")
		}
		var p map[string]string
		if err := json.Unmarshal(got.Payload, &p); err != nil || p["role"] != "assistant" {
			t.Errorf("role not carried: %s (%v)", got.Payload, err)
		}
	})

	// Real payloads captured from public.agent_task_events for task
	// a98420c4 (issue #1206's live verification run), the concrete shapes
	// that motivated every filtered case below. Fixtures over invented
	// shapes: a guessed shape is exactly what let #1202's stub ship the
	// original defect.
	t.Run("real captured payloads", func(t *testing.T) {
		systemPrompt := json.RawMessage(`{"id":"b2fad3c8-653b-4593-aeab-75e633234218","kind":"SystemPromptEvent","tools":[{"kind":"TerminalTool","title":"terminal"}]}`)
		lastUserMsgID := json.RawMessage(`{"id":"f0fdca9f-10b4-4cf8-9532-028e91bc4cd5","key":"last_user_message_id","kind":"ConversationStateUpdateEvent","value":"60eb8503-ec6b-4e41-a067-2b5ab7a6439d","source":"environment","timestamp":"2026-08-26T01:43:08.336259"}`)
		execStatus := json.RawMessage(`{"id":"65dac71f-397a-461f-9e69-cbd50af7ea61","key":"execution_status","kind":"ConversationStateUpdateEvent","value":"running","source":"environment","timestamp":"2026-08-26T01:43:08.364442"}`)

		filtered := []struct {
			name string
			in   SandboxEvent
		}{
			{"SystemPromptEvent", SandboxEvent{ID: "b2fad3c8-653b-4593-aeab-75e633234218", Kind: "SystemPromptEvent", Raw: systemPrompt}},
			{"ConversationStateUpdateEvent last_user_message_id", SandboxEvent{ID: "f0fdca9f-10b4-4cf8-9532-028e91bc4cd5", Kind: "ConversationStateUpdateEvent", Raw: lastUserMsgID}},
			{"ConversationStateUpdateEvent execution_status", SandboxEvent{ID: "65dac71f-397a-461f-9e69-cbd50af7ea61", Kind: "ConversationStateUpdateEvent", Raw: execStatus}},
			{"user prompt echo", SandboxEvent{ID: "u-echo", Kind: "MessageEvent", Source: "user", TextPreview: "Run `ls -la` in the workspace and tell me exactly what files are present."}},
		}
		for _, tc := range filtered {
			t.Run(tc.name, func(t *testing.T) {
				if _, ok := mapSandboxEvent(tc.in); ok {
					t.Errorf("%s must be filtered out of the rendered stream, not stored", tc.name)
				}
			})
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

// TestEventSyncerFinishVanishedSyncsTailEvents is the regression guard for
// issue #1206's root cause: finishVanished used to emit only a bare terminal
// status event and never pulled sandbox events or the workspace listing, so
// any tool_call/tool_result/file activity that happened between a task's
// last active pass and it going terminal (a short task's real work often
// concentrates exactly there) was lost, never retried, permanently. This
// proves the second pass, where the task has already left ListActive, still
// carries the tail activity through.
func TestEventSyncerFinishVanishedSyncsTailEvents(t *testing.T) {
	tenant, user, id := uuid.New(), uuid.New(), uuid.New()
	task := Task{ID: id, TenantID: tenant, UserID: user, Status: StatusRunning, EngineSessionRef: "sess-3"}
	repo := &fakeRepoForSync{
		listActive: []Task{task},
		get: map[uuid.UUID]Task{
			id: {ID: id, TenantID: tenant, UserID: user, Status: StatusSucceeded, EngineSessionRef: "sess-3"},
		},
	}
	// Pass 1: nothing has happened in the sandbox yet.
	src := &fakeEventSource{
		events: func(context.Context, string) ([]SandboxEvent, error) { return nil, nil },
		files:  func(context.Context, string) ([]WorkspaceFile, error) { return nil, nil },
	}
	s := NewEventSyncer(repo, src, PollerConfig{})
	mustNil(t, s.RunOnce(context.Background()))

	// Between pass 1 and pass 2 the task ran its real work and went
	// terminal: it drops out of ListActive, and the sandbox now has the
	// tool call, its result, and the file it wrote.
	repo.listActive = nil
	src.events = func(context.Context, string) ([]SandboxEvent, error) {
		return []SandboxEvent{
			{ID: "act-1", Kind: "ActionEvent", ToolName: "terminal", ToolCallID: "c1"},
			{ID: "obs-1", Kind: "ObservationEvent", ToolName: "terminal", ToolCallID: "c1"},
		}, nil
	}
	src.files = func(context.Context, string) ([]WorkspaceFile, error) {
		return []WorkspaceFile{{Name: "notes.md", Size: 42, ModTime: time.Now()}}, nil
	}
	mustNil(t, s.RunOnce(context.Background()))

	var sawToolCall, sawToolResult, sawFile, sawTerminal bool
	for _, batch := range repo.appends {
		for _, ev := range batch {
			switch {
			case ev.Kind == EventToolCall:
				sawToolCall = true
			case ev.Kind == EventToolResult:
				sawToolResult = true
			case ev.Kind == EventFile && strings.Contains(string(ev.Payload), "notes.md"):
				sawFile = true
			case ev.Kind == EventStatus && strings.Contains(string(ev.Payload), "succeeded"):
				sawTerminal = true
			}
		}
	}
	if !sawToolCall || !sawToolResult || !sawFile {
		t.Errorf("tail activity lost: tool_call=%v tool_result=%v file=%v", sawToolCall, sawToolResult, sawFile)
	}
	if !sawTerminal {
		t.Error("terminal status event never emitted for the vanished task")
	}
}

// TestEventSyncerFiltersHiddenWorkspaceFiles guards the ".git listing reads
// as a step" half of issue #1206: a dot-prefixed workspace entry is scaffold
// noise, not agent output, and must never become a rendered file event.
func TestEventSyncerFiltersHiddenWorkspaceFiles(t *testing.T) {
	tenant, user := uuid.New(), uuid.New()
	task := Task{ID: uuid.New(), TenantID: tenant, UserID: user, Status: StatusRunning, EngineSessionRef: "sess-4"}
	src := &fakeEventSource{
		events: func(context.Context, string) ([]SandboxEvent, error) { return nil, nil },
		files: func(context.Context, string) ([]WorkspaceFile, error) {
			return []WorkspaceFile{
				{Name: ".git", Size: 4096, ModTime: time.Now()},
				{Name: "notes.md", Size: 12, ModTime: time.Now()},
			}, nil
		},
	}
	repo := &fakeRepoForSync{listActive: []Task{task}}
	s := NewEventSyncer(repo, src, PollerConfig{})
	mustNil(t, s.RunOnce(context.Background()))

	var names []string
	for _, ev := range repo.appends[0] {
		if ev.Kind == EventFile {
			names = append(names, string(ev.Payload))
		}
	}
	if len(names) != 1 || !strings.Contains(names[0], "notes.md") {
		t.Errorf("file events = %v, want exactly notes.md (.git filtered out)", names)
	}
}

func mustNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNormalizeSourceID(t *testing.T) {
	seed := strings.Repeat("x", 600)
	got := normalizeSourceID(seed, seed)
	if len(got) > 52 || !strings.HasPrefix(got, "sha256:") {
		t.Errorf("overlong id not folded: %q", got)
	}
	if got != normalizeSourceID("", seed) {
		t.Error("empty and overlong ids must fold to the same derived key for identical seeds")
	}
	if normalizeSourceID("abc", "ignored") != "abc" {
		t.Error("a short present id must pass through unchanged")
	}
}

// ---------------------------------------------------------------------------
// Incremental pulls (issues #1622, #1504)
//
// The syncer runs often enough now that a step reaches the transcript while
// the step is still happening, which is the whole point of the change. What
// makes that affordable is this: AppendEvents issues one INSERT per event
// inside one transaction holding a per-task advisory lock, so re-appending
// the run's entire history every pass and leaning on the dedup index to throw
// it away is work that grows with the square of the run's length. It was
// already the shape at the old cadence; at the new one it would be the reason
// to refuse the new cadence.
// ---------------------------------------------------------------------------

func TestEventSyncer_AppendsOnlyTheStepsItHasNotStoredYet(t *testing.T) {
	task := Task{ID: uuid.New(), TenantID: uuid.New(), UserID: uuid.New(),
		Status: StatusRunning, EngineSessionRef: "session-1"}
	repo := &fakeRepoForSync{listActive: []Task{task}, get: map[uuid.UUID]Task{task.ID: task}}

	all := []SandboxEvent{
		{ID: "e1", Kind: "ActionEvent", ToolName: "bash", ToolCallID: "c1", TextPreview: "list the workspace"},
		{ID: "e2", Kind: "ObservationEvent", ToolName: "bash", ToolCallID: "c1", TextPreview: "AGENTS.md"},
		{ID: "e3", Kind: "ActionEvent", ToolName: "str_replace_editor", ToolCallID: "c2", TextPreview: "write sixcap.txt"},
	}
	// Pass one sees the first two, pass two sees all three: the shape of a
	// live conversation's event store, which only ever grows.
	visible := 2
	src := &fakeEventSource{
		events: func(context.Context, string) ([]SandboxEvent, error) { return all[:visible], nil },
		files:  func(context.Context, string) ([]WorkspaceFile, error) { return nil, nil },
	}
	syncer := NewEventSyncer(repo, src, PollerConfig{})

	if err := syncer.RunOnce(context.Background()); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	visible = 3
	if err := syncer.RunOnce(context.Background()); err != nil {
		t.Fatalf("second pass: %v", err)
	}

	if len(repo.appends) != 2 {
		t.Fatalf("appends=%d, want one per pass", len(repo.appends))
	}
	second := repo.appends[1]
	var sandboxIDs []string
	for _, ev := range second {
		if strings.HasPrefix(ev.SourceEventID, statusEventPrefix) {
			continue // the synthetic status row rides on every pass by design
		}
		sandboxIDs = append(sandboxIDs, ev.SourceEventID)
	}
	if len(sandboxIDs) != 1 || sandboxIDs[0] != "e3" {
		t.Fatalf("second pass appended %v, want only the step that is new since the first pass", sandboxIDs)
	}
}

func TestEventSyncer_TerminalFlushReconcilesTheWholeRun(t *testing.T) {
	// The incremental pull above is an optimisation over a source this
	// process does not control, so the one pass that has to be complete, the
	// last one, reads the run from the beginning. Dedup makes the overlap
	// free and anything the incremental pulls missed still lands.
	task := Task{ID: uuid.New(), TenantID: uuid.New(), UserID: uuid.New(),
		Status: StatusRunning, EngineSessionRef: "session-1"}
	repo := &fakeRepoForSync{listActive: []Task{task}, get: map[uuid.UUID]Task{task.ID: task}}

	all := []SandboxEvent{
		{ID: "e1", Kind: "ActionEvent", ToolName: "bash", ToolCallID: "c1", TextPreview: "list"},
		{ID: "e2", Kind: "ObservationEvent", ToolName: "bash", ToolCallID: "c1", TextPreview: "ok"},
	}
	src := &fakeEventSource{
		events: func(context.Context, string) ([]SandboxEvent, error) { return all, nil },
		files:  func(context.Context, string) ([]WorkspaceFile, error) { return nil, nil },
	}
	syncer := NewEventSyncer(repo, src, PollerConfig{})

	if err := syncer.RunOnce(context.Background()); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	repo.appends = nil
	syncer.FlushTask(context.Background(), task)

	if len(repo.appends) != 1 {
		t.Fatalf("appends=%d, want one flush", len(repo.appends))
	}
	var ids []string
	for _, ev := range repo.appends[0] {
		ids = append(ids, ev.SourceEventID)
	}
	if len(ids) != 2 || ids[0] != "e1" || ids[1] != "e2" {
		t.Fatalf("flush appended %v, want the whole run", ids)
	}
}

func TestEventSyncer_FlushTaskIgnoresATaskWithNoSession(t *testing.T) {
	repo := &fakeRepoForSync{}
	called := false
	src := &fakeEventSource{
		events: func(context.Context, string) ([]SandboxEvent, error) {
			called = true
			return nil, nil
		},
		files: func(context.Context, string) ([]WorkspaceFile, error) { return nil, nil },
	}
	NewEventSyncer(repo, src, PollerConfig{}).
		FlushTask(context.Background(), Task{ID: uuid.New(), Status: StatusQueued})

	if called || len(repo.appends) != 0 {
		t.Fatal("a task that never launched has no session to read events from")
	}
}

func TestEventSyncer_DoesNotRewriteAWorkspaceFileThatHasNotChanged(t *testing.T) {
	// The workspace listing arrives whole on every pass, so without this the
	// per-pass write grows with the number of files the run produced: the same
	// square-of-the-run cost the sandbox-event offset removes, by the other
	// door.
	task := Task{ID: uuid.New(), TenantID: uuid.New(), UserID: uuid.New(),
		Status: StatusRunning, EngineSessionRef: "session-1"}
	repo := &fakeRepoForSync{listActive: []Task{task}, get: map[uuid.UUID]Task{task.ID: task}}

	files := []WorkspaceFile{{Name: "sixcap.txt", Size: 13, ModTime: time.Unix(1000, 0)}}
	src := &fakeEventSource{
		events: func(context.Context, string) ([]SandboxEvent, error) { return nil, nil },
		files:  func(context.Context, string) ([]WorkspaceFile, error) { return files, nil },
	}
	syncer := NewEventSyncer(repo, src, PollerConfig{})

	if err := syncer.RunOnce(context.Background()); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if countKind(repo.appends[0], EventFile) != 1 {
		t.Fatalf("first pass recorded %d file events, want 1", countKind(repo.appends[0], EventFile))
	}

	if err := syncer.RunOnce(context.Background()); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if got := countKind(repo.appends[1], EventFile); got != 0 {
		t.Fatalf("second pass rewrote %d unchanged file events", got)
	}

	// A rewritten file is a different fact and still lands: same name, new
	// size and mtime, so a new deterministic id.
	files = []WorkspaceFile{{Name: "sixcap.txt", Size: 26, ModTime: time.Unix(2000, 0)}}
	if err := syncer.RunOnce(context.Background()); err != nil {
		t.Fatalf("third pass: %v", err)
	}
	if got := countKind(repo.appends[2], EventFile); got != 1 {
		t.Fatalf("third pass recorded %d file events for a rewritten file, want 1", got)
	}
}

func countKind(events []TaskEvent, kind TaskEventKind) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}
