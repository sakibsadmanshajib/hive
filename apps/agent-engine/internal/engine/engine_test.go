package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/artifactsclient"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/controlclient"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/quota"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/sandbox"
)

// fakeAgentServer stands in for the real OpenHands agent-server (Python,
// unavailable in this test environment) behind the control socket, so
// SandboxEngine's control-channel wiring is exercised without Apptainer.
// It also implements process, acting as the fake sandbox subprocess's
// handle: Kill tears down the fake server the same way killing the real
// sandbox process would take the agent-server down with it.
// fixtureCredential is the obviously fake value the LLM settings tests send
// where a real deployment sends a gateway key.
const fixtureCredential = "test-key-not-a-real-one"

// perTaskFixtureCredential stands in for the credential control-plane mints on
// one task's own tenant billing account (#1507). Deliberately different from
// fixtureCredential, so a test can tell "spent the task's key" apart from
// "spent the process-wide key", which is the whole distinction.
const perTaskFixtureCredential = "test-per-task-key-not-a-real-one"

type fakeAgentServer struct {
	mu              sync.Mutex
	conversationID  uuid.UUID
	executionStatus controlclient.ExecutionStatus
	finalResponse   string
	ran             bool
	interrupted     bool
	killed          bool
	startReq        controlclient.StartConversationRequest
	startBody       []byte

	// finalStarted and finalGate, when set, let a test observe exactly when
	// the final-response fetch begins and control when it returns, which is
	// the point inside finishTerminal immediately before
	// publishDeckArtifact reads the session's bearer JWT.
	finalStarted chan struct{}
	finalGate    chan struct{}

	listener net.Listener
	srv      *http.Server

	// events backs the /events/search route: one page, no next_page_id.
	// setEvents lets a test populate it; issue #1206's regression coverage
	// is the only user today.
	events []json.RawMessage
}

func newFakeAgentServer(controlDir string) (*fakeAgentServer, error) {
	f := &fakeAgentServer{
		conversationID:  uuid.New(),
		executionStatus: controlclient.StatusIdle,
	}
	l, err := net.Listen("unix", filepath.Join(controlDir, sandbox.ControlSocketFileName))
	if err != nil {
		return nil, err
	}
	f.listener = l

	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		// Kept as raw bytes as well as decoded: a decoded struct proves the
		// Go field was populated, not that the JSON name the agent-server
		// actually reads was on the wire.
		f.startBody, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
		_ = json.Unmarshal(f.startBody, &f.startReq)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(controlclient.ConversationInfo{
			ID:              f.conversationID,
			ExecutionStatus: f.executionStatus,
		})
	})
	convoPrefix := "/api/conversations/" + f.conversationID.String()
	mux.HandleFunc(convoPrefix+"/run", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.ran = true
		f.executionStatus = controlclient.StatusRunning
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
	})
	mux.HandleFunc(convoPrefix+"/interrupt", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.interrupted = true
		f.executionStatus = controlclient.StatusPaused
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
	})
	mux.HandleFunc(convoPrefix+"/events/search", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		items := f.events
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "next_page_id": ""})
	})
	mux.HandleFunc(convoPrefix+"/agent_final_response", func(w http.ResponseWriter, r *http.Request) {
		// Read the gates and then release f.mu before blocking on them: a
		// test that cancels during this window drives Interrupt through this
		// same server, and holding f.mu across the wait would deadlock it
		// rather than exercise the concurrency under test.
		f.mu.Lock()
		started, gate := f.finalStarted, f.finalGate
		f.mu.Unlock()
		if started != nil {
			started <- struct{}{}
		}
		if gate != nil {
			<-gate
		}

		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"response": f.finalResponse})
	})
	mux.HandleFunc(convoPrefix, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(controlclient.ConversationInfo{
			ID:              f.conversationID,
			ExecutionStatus: f.executionStatus,
		})
	})

	f.srv = &http.Server{Handler: mux}
	go func() { _ = f.srv.Serve(l) }()
	return f, nil
}

func (f *fakeAgentServer) setStatus(s controlclient.ExecutionStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.executionStatus = s
}

func (f *fakeAgentServer) setFinalGate(started, gate chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finalStarted, f.finalGate = started, gate
}

func (f *fakeAgentServer) setFinalResponse(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finalResponse = s
}

// setEvents populates the /events/search fixture from plain kind/field maps,
// e.g. {"kind": "ActionEvent", "tool_name": "terminal", "tool_call_id": "1"}.
func (f *fakeAgentServer) setEvents(items ...map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = make([]json.RawMessage, len(items))
	for i, item := range items {
		enc, err := json.Marshal(item)
		if err != nil {
			panic(err) // test fixture construction only
		}
		f.events[i] = enc
	}
}

func (f *fakeAgentServer) wasRun() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ran
}

func (f *fakeAgentServer) wasInterrupted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.interrupted
}

func (f *fakeAgentServer) wasKilled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.killed
}

func (f *fakeAgentServer) startConversationRequest() controlclient.StartConversationRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.startReq
}

// launchLLMFields returns the agent_settings.llm object from the launch body
// as the raw fields that were actually on the wire, keyed by their JSON names.
//
// Scoped to that object rather than searched for in the whole body, so a
// "stream" field some future nested object adds cannot satisfy the assertion,
// and returned as fields rather than as text so a failure can name the keys
// without printing api_key. Anyone who ever points these tests at a real
// credential would otherwise publish it in a CI log.
func (f *fakeAgentServer) launchLLMFields(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	f.mu.Lock()
	raw := append([]byte(nil), f.startBody...)
	f.mu.Unlock()

	var body struct {
		AgentSettings struct {
			LLM map[string]json.RawMessage `json:"llm"`
		} `json:"agent_settings"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode launch body: %v", err)
	}
	if body.AgentSettings.LLM == nil {
		t.Fatal("launch body carries no agent_settings.llm object")
	}
	return body.AgentSettings.LLM
}

func (f *fakeAgentServer) Kill() error {
	f.mu.Lock()
	f.killed = true
	f.mu.Unlock()
	return f.srv.Close()
}

// extractControlDir recovers the host-side control socket directory Launch
// passed to sandbox.BuildArgv, by finding the --bind pair whose container
// target is sandbox.ControlSocketContainerDir. Mirrors how the real in-SIF
// shim would learn nothing from argv at all (it only knows its own fixed
// container-side path) — this is test-only plumbing to let the fake stand
// in for both the shim and the agent-server behind it.
func extractControlDir(argv []string) (string, error) {
	suffix := ":" + sandbox.ControlSocketContainerDir
	for _, a := range argv {
		if strings.HasSuffix(a, suffix) {
			return strings.TrimSuffix(a, suffix), nil
		}
	}
	return "", fmt.Errorf("test: no control socket bind found in argv: %v", argv)
}

// shortTempDir creates a temp directory with a short auto-generated name
// directly under os.TempDir(), not nested under t.TempDir()'s test-name-
// derived path: Config.RunDir only ever holds Unix domain sockets, whose
// sun_path is capped at ~108 bytes on Linux, and a long test function name
// nested several directories deep blows that budget in practice.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newTestEngine(t *testing.T, captured **fakeAgentServer) *SandboxEngine {
	t.Helper()
	return newTestEngineWithQuota(t, captured, 4, 2)
}

// Pack fixture bodies. Deliberately not the real pack text: an assertion that
// matched the shipped AGENTS.md would also pass if Launch copied some other
// pack, and these strings are unique enough that finding them inside the
// agent's working directory can only mean this pack got there.
const (
	packFixtureAgentsMD = "# Fixture pack\n\nfixture-pack-agents-md-body\n"
	packFixtureSkillMD  = "---\nname: fixture-skill\ndescription: Fixture skill.\n---\n\nfixture-pack-skill-body\n"
)

// seededPacksDir is a stand-in for a real deployment's
// HIVE_AGENT_ENGINE_PACKS_DIR, which holds every pack. An empty directory
// would exercise a launch no deployment performs (and, since #1360, one that
// fails closed).
func seededPacksDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, pack := range []string{"coding-pack", packKnowledgeWork} {
		seedPack(t, dir, pack)
	}
	return dir
}

// seedPack writes one pack into packsDir in the layout the vendored OpenHands
// SDK loads from a conversation's working directory: AGENTS.md at the root
// (Skill.PATH_TO_THIRD_PARTY_SKILL_NAME) and .agents/skills/<name>/SKILL.md
// (USER/project skills dirs in openhands/sdk/skills/skill.py).
func seedPack(t *testing.T, packsDir, pack string) {
	t.Helper()
	skillDir := filepath.Join(packsDir, pack, ".agents", "skills", "fixture-skill")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatalf("seed pack %s: %v", pack, err)
	}
	writes := map[string]string{
		filepath.Join(packsDir, pack, "AGENTS.md"): packFixtureAgentsMD,
		filepath.Join(skillDir, "SKILL.md"):        packFixtureSkillMD,
	}
	for path, body := range writes {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("seed pack %s: %v", pack, err)
		}
	}
}

func newTestEngineWithQuota(t *testing.T, captured **fakeAgentServer, tenantConcurrency, userConcurrency int) *SandboxEngine {
	t.Helper()
	cfg := Config{
		QuotaTenantConcurrency: tenantConcurrency,
		QuotaUserConcurrency:   userConcurrency,

		SIFPath:       "/fake/agent-server.sif",
		PacksDir:      seededPacksDir(t),
		WorkspaceRoot: t.TempDir(),
		RunDir:        shortTempDir(t),
		ResolveEgressHosts: func(ctx context.Context, tenantID, userID uuid.UUID) ([]string, error) {
			return nil, nil
		},
		AgentProfileID:      uuid.New(),
		ControlReadyTimeout: 5 * time.Second,
	}
	e := New(cfg)
	e.start = func(argv []string) (process, error) {
		controlDir, err := extractControlDir(argv)
		if err != nil {
			return nil, err
		}
		f, err := newFakeAgentServer(controlDir)
		if err != nil {
			return nil, err
		}
		*captured = f
		return f, nil
	}
	return e
}

func testTask() Task {
	return Task{ID: uuid.New(), TenantID: uuid.New(), UserID: uuid.New(), Pack: "coding-pack"}
}

// Issue #308: the quota package existed but nothing on the production launch
// path ever acquired a slot, so one tenant could saturate the box.
func TestSandboxEngine_Launch_EnforcesTenantQuota(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngineWithQuota(t, &fake, 1, 1)

	first := testTask()
	if _, err := e.Launch(context.Background(), first); err != nil {
		t.Fatalf("first Launch: %v", err)
	}

	// Same tenant, different user: isolates the tenant ceiling from the
	// per-user one.
	second := testTask()
	second.TenantID = first.TenantID
	if _, err := e.Launch(context.Background(), second); !errors.Is(err, quota.ErrTenantQuotaExceeded) {
		t.Fatalf("expected ErrTenantQuotaExceeded, got %v", err)
	}
}

func TestSandboxEngine_Launch_EnforcesUserQuota(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngineWithQuota(t, &fake, 4, 1)

	first := testTask()
	if _, err := e.Launch(context.Background(), first); err != nil {
		t.Fatalf("first Launch: %v", err)
	}

	second := testTask()
	second.TenantID = first.TenantID
	second.UserID = first.UserID
	if _, err := e.Launch(context.Background(), second); !errors.Is(err, quota.ErrUserQuotaExceeded) {
		t.Fatalf("expected ErrUserQuotaExceeded, got %v", err)
	}
}

// The agent-server is a server: it keeps running after the conversation
// finishes, and apps/control-plane/internal/agenttask's poller only records
// the terminal status, it never calls Cancel. Without reaping here every
// completed task leaked its sandbox process, its session directories and
// (once quota is wired) its concurrency slot forever.
func TestSandboxEngine_Status_TerminalReapsSandboxAndFreesQuota(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngineWithQuota(t, &fake, 1, 1)

	task := testTask()
	sessionRef, err := e.Launch(context.Background(), task)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	first := fake

	first.setFinalResponse("done")
	first.setStatus(controlclient.StatusFinished)

	status, summary, _, err := e.Status(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != StatusSucceeded || summary != "done" {
		t.Fatalf("expected succeeded/done, got %s/%q", status, summary)
	}
	if !first.wasKilled() {
		t.Fatal("expected terminal Status to kill the sandbox process")
	}

	// The poller retries Status whenever its own Transition call failed, so a
	// reaped session must keep answering instead of becoming unknown.
	status, summary, _, err = e.Status(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("repeat Status after reap: %v", err)
	}
	if status != StatusSucceeded || summary != "done" {
		t.Fatalf("expected repeat Status to still report succeeded/done, got %s/%q", status, summary)
	}

	// The freed slot is the point: the same tenant/user can launch again.
	next := testTask()
	next.TenantID = task.TenantID
	next.UserID = task.UserID
	if _, err := e.Launch(context.Background(), next); err != nil {
		t.Fatalf("expected quota slot freed by the reap, got %v", err)
	}
}

// Issue #1206: pulling a finished task's tail events and workspace listing
// AFTER it leaves the active set always raced the sandbox tearing its own
// control socket and /workspace bind mount down, and always lost — two live
// runs on the deployed box, 2 for 2. The reason is structural, not timing
// luck: the same Status() call that discovers a terminal execStatus is what
// triggers reap(), synchronously, before this or any other caller can ever
// observe the task as terminal. So Events/Files must serve a snapshot
// captured before that teardown, never a live pull after it.
func TestSandboxEngine_Status_CapturesEventsAndFilesBeforeTeardown(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)

	task := testTask()
	sessionRef, err := e.Launch(context.Background(), task)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	// Written to the sandbox's /workspace bind mount before it goes
	// terminal, standing in for a real task's tool output.
	workingDir := filepath.Join(e.cfg.WorkspaceRoot, task.ID.String())
	if err := os.WriteFile(filepath.Join(workingDir, "notes.md"), []byte("done"), 0o600); err != nil {
		t.Fatalf("seed workspace file: %v", err)
	}
	fake.setEvents(
		map[string]any{"kind": "ActionEvent", "id": "a1", "tool_name": "terminal", "tool_call_id": "c1"},
		map[string]any{"kind": "ObservationEvent", "id": "o1", "tool_name": "terminal", "tool_call_id": "c1"},
	)
	fake.setFinalResponse("done")
	fake.setStatus(controlclient.StatusFinished)

	status, _, _, err := e.Status(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != StatusSucceeded {
		t.Fatalf("expected succeeded, got %s", status)
	}
	if !fake.wasKilled() {
		t.Fatal("expected terminal Status to kill the sandbox process")
	}

	// The control socket is dead (fake.Kill closed its listener) and
	// workingDir is removed (reap's os.RemoveAll) by this point. A live pull
	// would either error or silently see an empty, already-deleted
	// directory; only the reap-time snapshot can produce this content.
	events, err := e.Events(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Events after reap: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 captured events surviving reap, got %d: %+v", len(events), events)
	}
	if events[0].Kind != "ActionEvent" || events[1].Kind != "ObservationEvent" {
		t.Fatalf("unexpected event kinds: %+v", events)
	}

	files, err := e.Files(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Files after reap: %v", err)
	}
	if len(files) != 1 || files[0].Name != "notes.md" {
		t.Fatalf("expected the seeded workspace file to survive reap, got %+v", files)
	}
}

// Same guarantee via Cancel's reap call, the other of reap's two callers.
func TestSandboxEngine_Cancel_CapturesEventsBeforeTeardown(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)

	sessionRef, err := e.Launch(context.Background(), testTask())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	fake.setEvents(map[string]any{"kind": "ActionEvent", "id": "a1", "tool_name": "terminal", "tool_call_id": "c1"})

	if err := e.Cancel(context.Background(), sessionRef); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	events, err := e.Events(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Events after cancel: %v", err)
	}
	if len(events) != 1 || events[0].Kind != "ActionEvent" {
		t.Fatalf("expected the pre-cancel event to survive reap, got %+v", events)
	}
}

func TestSandboxEngine_Launch_StartsAndRunsConversation(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)

	sessionRef, err := e.Launch(context.Background(), testTask())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if _, err := uuid.Parse(sessionRef); err != nil {
		t.Fatalf("expected sessionRef to be a UUID, got %q: %v", sessionRef, err)
	}
	if !fake.wasRun() {
		t.Fatal("expected Launch to call POST .../run")
	}
}

func TestSandboxEngine_Launch_PassesInstructionsAsInitialMessage(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)

	task := testTask()
	task.Instructions = "Skill: refactor\nClean up the auth module."
	if _, err := e.Launch(context.Background(), task); err != nil {
		t.Fatalf("Launch: %v", err)
	}

	req := fake.startConversationRequest()
	if req.InitialMessage == nil {
		t.Fatal("expected StartConversation to carry an initial_message")
	}
	if len(req.InitialMessage.Content) != 1 || req.InitialMessage.Content[0].Text != task.Instructions {
		t.Fatalf("got initial_message content %+v, want text %q", req.InitialMessage.Content, task.Instructions)
	}
}

func TestSandboxEngine_Launch_OmitsInitialMessageWhenNoInstructions(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)

	if _, err := e.Launch(context.Background(), testTask()); err != nil {
		t.Fatalf("Launch: %v", err)
	}

	if req := fake.startConversationRequest(); req.InitialMessage != nil {
		t.Fatalf("expected no initial_message when Task.Instructions is empty, got %+v", req.InitialMessage)
	}
}

func TestSandboxEngine_Launch_RequiresPack(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)

	task := testTask()
	task.Pack = ""
	if _, err := e.Launch(context.Background(), task); err == nil {
		t.Fatal("expected error for empty Task.Pack")
	}
	if fake != nil {
		t.Fatal("expected no sandbox process to start for an invalid task")
	}
}

func TestSandboxEngine_Launch_FailsClosedWhenEgressResolutionFails(t *testing.T) {
	e := New(Config{
		SIFPath:       "/fake/agent-server.sif",
		PacksDir:      t.TempDir(),
		WorkspaceRoot: t.TempDir(),
		RunDir:        t.TempDir(),
		ResolveEgressHosts: func(ctx context.Context, tenantID, userID uuid.UUID) ([]string, error) {
			return nil, errors.New("control-plane unreachable")
		},
	})
	started := false
	e.start = func(argv []string) (process, error) {
		started = true
		return nil, nil
	}

	if _, err := e.Launch(context.Background(), testTask()); err == nil {
		t.Fatal("expected Launch to fail closed when egress policy cannot be resolved")
	}
	if started {
		t.Fatal("expected sandbox not to start when egress policy resolution fails")
	}
}

func TestSandboxEngine_Status_MapsFinishedToSucceeded(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)
	sessionRef, err := e.Launch(context.Background(), testTask())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	fake.setStatus(controlclient.StatusFinished)
	fake.setFinalResponse("all done")

	status, summary, errMsg, err := e.Status(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != StatusSucceeded {
		t.Fatalf("got status %q, want %q", status, StatusSucceeded)
	}
	if summary != "all done" {
		t.Fatalf("got summary %q, want %q", summary, "all done")
	}
	if errMsg != "" {
		t.Fatalf("expected no error message on success, got %q", errMsg)
	}
}

func TestSandboxEngine_Status_MapsErrorToFailed(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)
	sessionRef, err := e.Launch(context.Background(), testTask())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	fake.setStatus(controlclient.StatusErrored)

	status, _, errMsg, err := e.Status(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != StatusFailed {
		t.Fatalf("got status %q, want %q", status, StatusFailed)
	}
	if errMsg == "" {
		t.Fatal("expected a non-empty error message for a failed session")
	}
}

func TestSandboxEngine_Status_DefaultsRunningWhileInProgress(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)
	sessionRef, err := e.Launch(context.Background(), testTask())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	// Launch already called Run, which the fake server maps to "running".
	status, _, _, err := e.Status(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != StatusRunning {
		t.Fatalf("got status %q, want %q", status, StatusRunning)
	}
}

func TestSandboxEngine_Status_UnknownSessionRef(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)

	_, _, _, err := e.Status(context.Background(), uuid.New().String())
	if !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("expected ErrUnknownSession, got %v", err)
	}
}

func TestSandboxEngine_Launch_CleansUpDirsOnFailure(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)
	e.start = func(argv []string) (process, error) {
		return nil, errors.New("boom")
	}

	if _, err := e.Launch(context.Background(), testTask()); err == nil {
		t.Fatal("expected Launch to fail")
	}

	assertDirEmpty(t, e.cfg.RunDir)
	assertDirEmpty(t, e.cfg.WorkspaceRoot)
}

func TestSandboxEngine_Cancel_CleansUpDirs(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)
	sessionRef, err := e.Launch(context.Background(), testTask())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	if err := e.Cancel(context.Background(), sessionRef); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	assertDirEmpty(t, e.cfg.RunDir)
	assertDirEmpty(t, e.cfg.WorkspaceRoot)
}

// Characterisation test for pre-existing behaviour, NOT regression coverage
// for issue #886. Nothing in this package changed in that fix, so every
// assertion below passes with the fix fully reverted; it exists to pin the
// invariant control-plane now depends on, which is that ending a session
// through Cancel returns its slot to the pool immediately rather than whenever
// the sandbox finishes on its own (roughly sixteen minutes on the demo box).
//
// The actual regression coverage for issue #886 is in
// apps/control-plane/internal/agenttask/service_test.go, where
// TestService_Cancel_ReleasesEngineConcurrencySlot and its two siblings fail
// on revert because they drive Service.Cancel and assert on counters that only
// move when Service reaches the engine.
func TestSandboxEngine_Cancel_FreesQuotaSlot(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngineWithQuota(t, &fake, 1, 1)

	task := testTask()
	sessionRef, err := e.Launch(context.Background(), task)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := e.Cancel(context.Background(), sessionRef); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	if tenant, user := e.q.InUse(task.TenantID, task.UserID); tenant != 0 || user != 0 {
		t.Fatalf("cancel left quota held: tenant=%d user=%d, want 0/0", tenant, user)
	}

	// The observable consequence: the same user can start work again at once.
	next := testTask()
	next.TenantID = task.TenantID
	next.UserID = task.UserID
	if _, err := e.Launch(context.Background(), next); err != nil {
		t.Fatalf("expected the cancelled session's slot to be reusable, got %v", err)
	}
}

func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected %s empty, got %v", dir, entries)
	}
}

func TestSandboxEngine_Cancel_InterruptsAndKillsProcess(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)
	sessionRef, err := e.Launch(context.Background(), testTask())
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	if err := e.Cancel(context.Background(), sessionRef); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if !fake.wasInterrupted() {
		t.Fatal("expected Cancel to call POST .../interrupt")
	}
	if !fake.wasKilled() {
		t.Fatal("expected Cancel to kill the sandbox process")
	}

	// A cancelled session keeps answering Status with its terminal outcome
	// rather than becoming unknown: agenttask's poller retries Status whenever
	// its own Transition failed, and an error there leaves the task active.
	status, _, _, err := e.Status(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Status after Cancel: %v", err)
	}
	if status != StatusCancelled {
		t.Fatalf("expected cancelled after Cancel, got %s", status)
	}
}

// Issue #1507: agent tasks charged their tenant nothing. The sandbox's model
// calls did reach the gateway and were metered there, but they carried
// Config.LLMAPIKey, one Hive-owned credential shared by every tenant, so the
// charge landed on that account and the customer who submitted the task was
// never billed. Control-plane now mints a credential on the task's own tenant
// billing account and sends it with the launch; this is the assertion that the
// launcher actually spends it.
//
// Both packs, because the live report observed the zero charge on
// knowledge-work-pack and coding-pack alike.
func TestSandboxEngine_Launch_PrefersThePerTaskCredentialOverTheProcessWideOne(t *testing.T) {
	for _, pack := range []string{"knowledge-work-pack", "coding-pack"} {
		t.Run(pack, func(t *testing.T) {
			var fake *fakeAgentServer
			e := newTestEngine(t, &fake)
			e.cfg.LLMModel = "openai/hive-test-model"
			e.cfg.LLMBaseURL = "https://gateway.example/v1"
			e.cfg.LLMAPIKey = fixtureCredential

			task := testTask()
			task.Pack = pack
			task.LLMAPIKey = perTaskFixtureCredential

			if _, err := e.Launch(context.Background(), task); err != nil {
				t.Fatalf("Launch: %v", err)
			}

			req := fake.startConversationRequest()
			if req.AgentSettings == nil {
				t.Fatal("expected inline agent_settings")
			}
			// Value and provenance in one comparison: the key the sandbox is
			// given must be the task's own, and must not be the process-wide
			// one. Checking only "not empty" would pass against the defect,
			// since the process-wide key is not empty either.
			got := req.AgentSettings.LLM.APIKey
			if got != perTaskFixtureCredential || got == e.cfg.LLMAPIKey {
				t.Fatalf("llm.api_key is the process-wide credential, so this task's tenant is charged nothing (#1507); want the per-task one")
			}
		})
	}
}

// A launcher newer than its control-plane still has to run: an empty per-task
// credential keeps the configured one, which is the behaviour that shipped
// before #1507 and is what a rolling deploy briefly produces.
func TestSandboxEngine_Launch_FallsBackToTheConfiguredCredentialWhenTheTaskCarriesNone(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)
	e.cfg.LLMModel = "openai/hive-test-model"
	e.cfg.LLMBaseURL = "https://gateway.example/v1"
	e.cfg.LLMAPIKey = fixtureCredential

	task := testTask()
	// Asserted absent on purpose, and stated here rather than left implicit:
	// this is the pre-#1507 shape, a task whose control-plane never minted a
	// credential, and the launcher must not refuse it. Refusing is
	// control-plane's job, where the attribution decision is actually made.
	task.LLMAPIKey = ""

	if _, err := e.Launch(context.Background(), task); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	req := fake.startConversationRequest()
	if req.AgentSettings == nil {
		t.Fatal("expected inline agent_settings")
	}
	if req.AgentSettings.LLM.APIKey != e.cfg.LLMAPIKey {
		t.Fatal("llm.api_key did not fall back to the configured credential")
	}
}

// Issue #780: a sandbox launched with --containall has no persisted profile
// store, so an agent_profile_id can only ever resolve to ProfileNotFound
// there. With an LLM configured, Launch must send the inline agent_settings
// payload instead, and must not send both (the agent-server's own validator
// rejects the combination outright).
func TestSandboxEngine_Launch_SendsInlineAgentSettingsWhenLLMConfigured(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)
	e.cfg.LLMModel = "openai/hive-test-model"
	e.cfg.LLMBaseURL = "https://gateway.example/v1"
	// Held in a named constant rather than assigned inline: the repository's
	// pre-commit credential scan matches any quoted literal assigned to an
	// api-key-shaped field, and a fixture string is not worth weakening that
	// pattern for.
	e.cfg.LLMAPIKey = fixtureCredential

	if _, err := e.Launch(context.Background(), testTask()); err != nil {
		t.Fatalf("Launch: %v", err)
	}

	req := fake.startConversationRequest()
	if req.AgentProfileID != nil {
		t.Fatalf("expected no agent_profile_id alongside agent_settings, got %v", req.AgentProfileID)
	}
	if req.AgentSettings == nil {
		t.Fatal("expected inline agent_settings")
	}
	if req.AgentSettings.AgentKind != "openhands" {
		t.Fatalf("agent_kind = %q, want openhands", req.AgentSettings.AgentKind)
	}
	if got := req.AgentSettings.LLM.Model; got != e.cfg.LLMModel {
		t.Fatalf("llm.model = %q, want %q", got, e.cfg.LLMModel)
	}
	if got := req.AgentSettings.LLM.BaseURL; got != e.cfg.LLMBaseURL {
		t.Fatalf("llm.base_url = %q, want %q", got, e.cfg.LLMBaseURL)
	}
	if got := req.AgentSettings.LLM.APIKey; got != e.cfg.LLMAPIKey {
		t.Fatal("llm.api_key did not round-trip")
	}
	if !req.AgentSettings.LLM.Stream {
		t.Fatal("llm.stream = false, want true")
	}
	// The agent-server reads the JSON name, not the Go field: without
	// "stream": true in the body it computes streaming_enabled = false and
	// publishes no StreamingDeltaEvent for the whole conversation. A decoded
	// assertion cannot see that, because encoding and decoding are symmetric
	// through the same struct, so renaming the tag keeps it green.
	fields := fake.launchLLMFields(t)
	if got := string(fields["stream"]); got != "true" {
		t.Fatalf(`agent_settings.llm has no "stream": true on the wire (got %q); field names sent: %v`,
			got, slices.Sorted(maps.Keys(fields)))
	}
}

// launchAgentContext returns the agent_context object the launch body carried,
// or nil when the body had no such key at all. Read out of the raw bytes for
// the same reason launchLLMFields is: decoding into the Go struct and back
// through the same tags is symmetric, so it stays green even if the JSON name
// the agent-server actually reads is renamed or dropped.
func (f *fakeAgentServer) launchAgentContext(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	f.mu.Lock()
	raw := append([]byte(nil), f.startBody...)
	f.mu.Unlock()

	var body struct {
		AgentSettings struct {
			AgentContext map[string]json.RawMessage `json:"agent_context"`
		} `json:"agent_settings"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode launch body: %v", err)
	}
	return body.AgentSettings.AgentContext
}

// The sandbox agent's system prompt was not set by Hive at all: the launch
// payload carried agent_kind, llm and optionally tools, and nothing else, so
// the prompt was whatever the vendored OpenHands default preset produced and
// there was no knob anywhere to shape it.
//
// This is the delivery assertion for that knob. It runs the real Launch path
// against the fake agent-server and reads the bytes that server received, so
// it fails if the configured value stops reaching the sandbox for any reason:
// a missed assignment in Launch, a wrong or dropped JSON tag, or a field
// silently omitted. Asserting that engine.Config has the field would prove
// none of that.
func TestSandboxEngine_Launch_SendsConfiguredSystemMessageSuffix(t *testing.T) {
	const suffix = "Always cite the file you changed."
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)
	e.cfg.LLMModel = "openai/hive-test-model"
	e.cfg.SystemMessageSuffix = suffix

	if _, err := e.Launch(context.Background(), testTask()); err != nil {
		t.Fatalf("Launch: %v", err)
	}

	ctxFields := fake.launchAgentContext(t)
	if ctxFields == nil {
		t.Fatal("launch body carried no agent_settings.agent_context at all")
	}
	var got string
	if err := json.Unmarshal(ctxFields["system_message_suffix"], &got); err != nil {
		t.Fatalf(`agent_context has no "system_message_suffix" on the wire; fields sent: %v`,
			slices.Sorted(maps.Keys(ctxFields)))
	}
	if got != suffix {
		t.Fatalf("system_message_suffix = %q, want %q", got, suffix)
	}
}

// Issue #1360, second half. Putting the pack in the working directory is not
// enough on its own: AgentContext.load_project_skills defaults to False
// (vendor/openhands/openhands-sdk/openhands/sdk/context/agent_context.py), and
// it is the only thing that makes LocalConversation read the workspace for
// AGENTS.md and .agents/skills at all
// (conversation/impl/local_conversation.py). Hive used to send agent_context
// only when a system-message suffix was configured, so on the arm that runs
// every real task the flag was False on every launch.
//
// Asserted on the raw launch body rather than on the Go struct, because the
// JSON name is what the sandbox reads, and asserted for both suffix states,
// because the suffix is unset on every deployment today.
func TestSandboxEngine_Launch_EnablesProjectSkillLoading(t *testing.T) {
	for _, suffix := range []string{"", "Always cite the file you changed."} {
		name := "no suffix configured"
		if suffix != "" {
			name = "suffix configured"
		}
		t.Run(name, func(t *testing.T) {
			var fake *fakeAgentServer
			e := newTestEngine(t, &fake)
			e.cfg.LLMModel = "openai/hive-test-model"
			e.cfg.SystemMessageSuffix = suffix

			if _, err := e.Launch(context.Background(), testTask()); err != nil {
				t.Fatalf("Launch: %v", err)
			}

			ctxFields := fake.launchAgentContext(t)
			if ctxFields == nil {
				t.Fatal("launch body carried no agent_settings.agent_context at all")
			}
			var enabled bool
			if err := json.Unmarshal(ctxFields["load_project_skills"], &enabled); err != nil {
				t.Fatalf(`agent_context has no "load_project_skills" on the wire; fields sent: %v`,
					slices.Sorted(maps.Keys(ctxFields)))
			}
			if !enabled {
				t.Fatal("load_project_skills = false; the pack in the workspace is never read")
			}
		})
	}
}

// The profile path stays intact for a deployment that does persist profiles.
func TestSandboxEngine_Launch_SendsProfileIDWhenNoLLMConfigured(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)

	if _, err := e.Launch(context.Background(), testTask()); err != nil {
		t.Fatalf("Launch: %v", err)
	}

	req := fake.startConversationRequest()
	if req.AgentSettings != nil {
		t.Fatalf("expected no agent_settings without an LLM configured, got %+v", req.AgentSettings)
	}
	if req.AgentProfileID == nil || *req.AgentProfileID != e.cfg.AgentProfileID {
		t.Fatalf("agent_profile_id = %v, want %v", req.AgentProfileID, e.cfg.AgentProfileID)
	}
}

// --- pack materialization (issue #1360) -----------------------------------

// The pack was bind-mounted read-only at /opt/hive/pack and baked into the SIF
// at /opt/hive/packs, and the vendored OpenHands SDK reads neither: it loads
// AGENTS.md and .agents/skills/ from the conversation's own working directory
// (openhands/sdk/skills/skill.py load_project_skills). Launch handed it a
// freshly created empty directory, so every real task ran with no pack context
// and none of the pack's skills.
//
// Asserting the pack exists on disk under PacksDir would prove nothing: it
// always did, and that is why the defect survived. This reads the bodies back
// out of the directory this launch bind-mounts as /workspace, at the exact
// paths the SDK loader walks.
func TestSandboxEngine_Launch_MaterializesPackIntoTheAgentWorkingDir(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)
	task := testTask()

	if _, err := e.Launch(context.Background(), task); err != nil {
		t.Fatalf("Launch: %v", err)
	}

	workingDir := filepath.Join(e.cfg.WorkspaceRoot, task.ID.String())
	for rel, want := range map[string]string{
		"AGENTS.md": packFixtureAgentsMD,
		filepath.Join(".agents", "skills", "fixture-skill", "SKILL.md"): packFixtureSkillMD,
	} {
		got, err := os.ReadFile(filepath.Join(workingDir, rel))
		if err != nil {
			t.Fatalf("pack file %s never reached the agent working directory: %v", rel, err)
		}
		if string(got) != want {
			t.Fatalf("pack file %s = %q, want %q", rel, got, want)
		}
	}
}

// Fail closed, loudly. A packs directory that does not hold the requested pack
// is a misconfigured deployment, and the outcome it used to produce was a task
// that ran anyway with an empty context and looked like a bad model.
func TestSandboxEngine_Launch_FailsWhenThePackIsMissing(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake)
	task := testTask()
	task.Pack = "no-such-pack"

	if _, err := e.Launch(context.Background(), task); err == nil {
		t.Fatal("expected Launch to fail with no pack directory to copy")
	}

	if _, err := os.Stat(filepath.Join(e.cfg.WorkspaceRoot, task.ID.String())); !os.IsNotExist(err) {
		t.Fatalf("failed launch left its working directory behind: %v", err)
	}
}

// Task.Pack names a directory under PacksDir, and it arrives over the launcher
// socket from control-plane. Control-plane validates it (agenttask.ErrInvalidPack),
// but this process is the one that turns it into a path, so it validates it too.
func TestSandboxEngine_Launch_RejectsPackNamesThatAreNotPackNames(t *testing.T) {
	for _, pack := range []string{"../coding-pack", "..", ".", "nested/coding-pack"} {
		t.Run(pack, func(t *testing.T) {
			var fake *fakeAgentServer
			e := newTestEngine(t, &fake)
			task := testTask()
			task.Pack = pack

			if _, err := e.Launch(context.Background(), task); err == nil {
				t.Fatalf("Launch accepted pack name %q", pack)
			}
		})
	}
}

// --- publishDeckArtifact (issue #312/#300 wiring) -------------------------

// fakePublisher stands in for *artifactsclient.Client. Records every call so
// tests can assert on exactly what reached it (in particular, the bearer
// JWT), without a real edge-api.
type fakePublisher struct {
	mu sync.Mutex

	createCalls     []struct{ bearerJWT, name, html string }
	addVersionCalls []struct{ bearerJWT, artifactID, html string }

	createErr     error
	addVersionErr error
	url           string

	// started and gate, when both non-nil, let a test observe exactly when
	// Create begins and control exactly when it returns — used to force a
	// deterministic interleaving of two concurrent Status calls instead of
	// hoping the scheduler produces one.
	started chan struct{}
	gate    chan struct{}
}

func (f *fakePublisher) Create(_ context.Context, bearerJWT, name, html string) (artifactsclient.Artifact, error) {
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls = append(f.createCalls, struct{ bearerJWT, name, html string }{bearerJWT, name, html})
	if f.createErr != nil {
		return artifactsclient.Artifact{}, f.createErr
	}
	return artifactsclient.Artifact{ID: "artifact-1", Version: 1, URL: f.url}, nil
}

func (f *fakePublisher) AddVersion(_ context.Context, bearerJWT, artifactID, html string) (artifactsclient.Artifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addVersionCalls = append(f.addVersionCalls, struct{ bearerJWT, artifactID, html string }{bearerJWT, artifactID, html})
	if f.addVersionErr != nil {
		return artifactsclient.Artifact{}, f.addVersionErr
	}
	return artifactsclient.Artifact{ID: artifactID, Version: 2, URL: f.url}, nil
}

// newTestEngineWithPublisher mirrors newTestEngineWithQuota but also exposes
// the workspace root, so a test can write a deck manifest into a launched
// session's /workspace before polling Status.
func newTestEngineWithPublisher(t *testing.T, captured **fakeAgentServer, publisher deckPublisher) (*SandboxEngine, string) {
	t.Helper()
	workspaceRoot := t.TempDir()
	cfg := Config{
		QuotaTenantConcurrency: 4,
		QuotaUserConcurrency:   2,

		SIFPath:       "/fake/agent-server.sif",
		PacksDir:      seededPacksDir(t),
		WorkspaceRoot: workspaceRoot,
		RunDir:        shortTempDir(t),
		ResolveEgressHosts: func(ctx context.Context, tenantID, userID uuid.UUID) ([]string, error) {
			return nil, nil
		},
		AgentProfileID:      uuid.New(),
		ControlReadyTimeout: 5 * time.Second,
		Publisher:           publisher,
	}
	e := New(cfg)
	e.start = func(argv []string) (process, error) {
		controlDir, err := extractControlDir(argv)
		if err != nil {
			return nil, err
		}
		f, err := newFakeAgentServer(controlDir)
		if err != nil {
			return nil, err
		}
		*captured = f
		return f, nil
	}
	return e, workspaceRoot
}

func writeDeckManifest(t *testing.T, workspaceRoot string, taskID uuid.UUID, manifest string) {
	t.Helper()
	dir := filepath.Join(workspaceRoot, taskID.String(), filepath.Dir(deckManifestRelPath))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}
	path := filepath.Join(workspaceRoot, taskID.String(), deckManifestRelPath)
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func knowledgeWorkTask() Task {
	t := testTask()
	t.Pack = packKnowledgeWork
	t.BearerJWT = "test-user-jwt"
	return t
}

func TestSandboxEngine_Status_PublishesDeckManifestAsArtifactURL(t *testing.T) {
	var fake *fakeAgentServer
	pub := &fakePublisher{url: "/artifacts/abc-123"}
	e, workspaceRoot := newTestEngineWithPublisher(t, &fake, pub)

	task := knowledgeWorkTask()
	sessionRef, err := e.Launch(context.Background(), task)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	writeDeckManifest(t, workspaceRoot, task.ID, `{"title":"Q3 Review","slides":[{"title":"Intro","bullets":["hi"]}]}`)

	fake.setFinalResponse("the agent's own text, not what should surface")
	fake.setStatus(controlclient.StatusFinished)

	status, summary, _, err := e.Status(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != StatusSucceeded {
		t.Fatalf("status = %s, want succeeded", status)
	}
	if summary != pub.url {
		t.Fatalf("resultSummary = %q, want the published artifact URL %q", summary, pub.url)
	}

	if len(pub.createCalls) != 1 {
		t.Fatalf("expected exactly one Create call, got %d", len(pub.createCalls))
	}
	call := pub.createCalls[0]
	if call.bearerJWT != task.BearerJWT {
		t.Fatalf("Create called with bearerJWT %q, want the task's own %q", call.bearerJWT, task.BearerJWT)
	}
	if call.name != "Q3 Review" {
		t.Fatalf("Create called with name %q, want the deck title", call.name)
	}
	if !strings.Contains(call.html, "Q3 Review") || !strings.Contains(call.html, "Intro") {
		t.Fatalf("Create called with html that does not look like the rendered deck: %q", call.html)
	}
}

func TestSandboxEngine_Status_AddsVersionWhenManifestNamesAnArtifact(t *testing.T) {
	var fake *fakeAgentServer
	pub := &fakePublisher{url: "/artifacts/existing-id"}
	e, workspaceRoot := newTestEngineWithPublisher(t, &fake, pub)

	task := knowledgeWorkTask()
	sessionRef, err := e.Launch(context.Background(), task)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	writeDeckManifest(t, workspaceRoot, task.ID,
		`{"title":"Q3 Review v2","slides":[{"title":"Intro","bullets":["hi"]}],"artifact_id":"existing-id"}`)

	fake.setFinalResponse("ignored")
	fake.setStatus(controlclient.StatusFinished)

	_, summary, _, err := e.Status(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if summary != pub.url {
		t.Fatalf("resultSummary = %q, want %q", summary, pub.url)
	}
	if len(pub.createCalls) != 0 {
		t.Fatalf("expected Create not to be called when artifact_id is set, got %d calls", len(pub.createCalls))
	}
	if len(pub.addVersionCalls) != 1 || pub.addVersionCalls[0].artifactID != "existing-id" {
		t.Fatalf("expected one AddVersion call against existing-id, got %+v", pub.addVersionCalls)
	}
}

// No manifest at all is the ordinary case for every coding-pack task, and
// for most knowledge-work-pack tasks that were not a deck request: the
// agent's own final-response text must reach the console unchanged.
func TestSandboxEngine_Status_NoManifestFallsBackToFinalResponse(t *testing.T) {
	var fake *fakeAgentServer
	pub := &fakePublisher{url: "/artifacts/should-not-be-used"}
	e, _ := newTestEngineWithPublisher(t, &fake, pub)

	task := knowledgeWorkTask()
	sessionRef, err := e.Launch(context.Background(), task)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	fake.setFinalResponse("plain text summary")
	fake.setStatus(controlclient.StatusFinished)

	_, summary, _, err := e.Status(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if summary != "plain text summary" {
		t.Fatalf("resultSummary = %q, want the agent's own final-response text", summary)
	}
	if len(pub.createCalls) != 0 || len(pub.addVersionCalls) != 0 {
		t.Fatal("expected no publish call when no manifest was written")
	}
}

// A task's own conversation already succeeded; a storage-side publish
// failure must not turn that into anything worse than losing the link.
func TestSandboxEngine_Status_PublishFailureFallsBackWithoutFailingTask(t *testing.T) {
	var fake *fakeAgentServer
	pub := &fakePublisher{createErr: errors.New("edge-api: 503")}
	e, workspaceRoot := newTestEngineWithPublisher(t, &fake, pub)

	task := knowledgeWorkTask()
	sessionRef, err := e.Launch(context.Background(), task)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	writeDeckManifest(t, workspaceRoot, task.ID, `{"title":"Doomed Deck","slides":[{"title":"S1","bullets":["x"]}]}`)

	fake.setFinalResponse("fallback text")
	fake.setStatus(controlclient.StatusFinished)

	status, summary, errMessage, err := e.Status(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status != StatusSucceeded || errMessage != "" {
		t.Fatalf("expected the task to still report succeeded with no error, got status=%s errMessage=%q", status, errMessage)
	}
	if summary != "fallback text" {
		t.Fatalf("resultSummary = %q, want the agent's own final-response text", summary)
	}
}

// A manifest larger than maxDeckManifestBytes is rejected outright rather
// than silently truncated and parsed as a partial deck.
func TestSandboxEngine_Status_OversizedManifestSkipsPublish(t *testing.T) {
	var fake *fakeAgentServer
	pub := &fakePublisher{url: "/artifacts/should-not-be-used"}
	e, workspaceRoot := newTestEngineWithPublisher(t, &fake, pub)

	task := knowledgeWorkTask()
	sessionRef, err := e.Launch(context.Background(), task)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	oversized := `{"title":"` + strings.Repeat("x", int(maxDeckManifestBytes)+1) + `","slides":[]}`
	writeDeckManifest(t, workspaceRoot, task.ID, oversized)

	fake.setFinalResponse("fallback text")
	fake.setStatus(controlclient.StatusFinished)

	_, summary, _, err := e.Status(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if summary != "fallback text" {
		t.Fatalf("resultSummary = %q, want the agent's own final-response text", summary)
	}
	if len(pub.createCalls) != 0 {
		t.Fatal("expected no publish call for an oversized manifest")
	}
}

// A coding-pack task must never trigger a publish attempt even if something
// happened to leave a file at the same well-known path — the pack check
// comes first.
func TestSandboxEngine_Status_CodingPackNeverPublishes(t *testing.T) {
	var fake *fakeAgentServer
	pub := &fakePublisher{url: "/artifacts/should-not-be-used"}
	e, workspaceRoot := newTestEngineWithPublisher(t, &fake, pub)

	task := testTask() // pack: "coding-pack"
	task.BearerJWT = "test-user-jwt"
	sessionRef, err := e.Launch(context.Background(), task)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	writeDeckManifest(t, workspaceRoot, task.ID, `{"title":"Not A Deck Task","slides":[{"title":"S1","bullets":["x"]}]}`)

	fake.setFinalResponse("build succeeded")
	fake.setStatus(controlclient.StatusFinished)

	_, summary, _, err := e.Status(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if summary != "build succeeded" {
		t.Fatalf("resultSummary = %q, want the agent's own final-response text", summary)
	}
	if len(pub.createCalls) != 0 {
		t.Fatal("expected no publish call for a coding-pack task")
	}
}

// No bearer JWT (an API-key-authenticated task create, or any other caller
// that never supplied one) must skip publishing rather than call edge-api
// with an empty Authorization value.
func TestSandboxEngine_Status_NoBearerJWTSkipsPublish(t *testing.T) {
	var fake *fakeAgentServer
	pub := &fakePublisher{url: "/artifacts/should-not-be-used"}
	e, workspaceRoot := newTestEngineWithPublisher(t, &fake, pub)

	task := testTask()
	task.Pack = packKnowledgeWork // BearerJWT left empty
	sessionRef, err := e.Launch(context.Background(), task)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	writeDeckManifest(t, workspaceRoot, task.ID, `{"title":"No JWT","slides":[{"title":"S1","bullets":["x"]}]}`)

	fake.setFinalResponse("fallback text")
	fake.setStatus(controlclient.StatusFinished)

	_, summary, _, err := e.Status(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if summary != "fallback text" {
		t.Fatalf("resultSummary = %q, want the agent's own final-response text", summary)
	}
	if len(pub.createCalls) != 0 {
		t.Fatal("expected no publish call with no bearer JWT")
	}
}

// A symlink at the manifest path, pointing outside the session's own
// /workspace, must never be followed. Without resolveWithinRoot this would
// let a knowledge-work-pack task (arbitrary shell access inside its
// sandbox) make the host process read and potentially publish any file its
// own OS user can see.
func TestSandboxEngine_Status_RefusesSymlinkedManifestOutsideWorkspace(t *testing.T) {
	var fake *fakeAgentServer
	pub := &fakePublisher{url: "/artifacts/should-not-be-used"}
	e, workspaceRoot := newTestEngineWithPublisher(t, &fake, pub)

	task := knowledgeWorkTask()
	sessionRef, err := e.Launch(context.Background(), task)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	// A file elsewhere on the host that happens to look exactly like a
	// valid, publishable deck manifest -- standing in for whatever real
	// secret the daemon's own OS user can read (its LLM key, the internal
	// service token). If the symlink is followed, this test would see it
	// published.
	outsideDir := t.TempDir()
	secretPath := filepath.Join(outsideDir, "not-yours.json")
	if err := os.WriteFile(secretPath, []byte(`{"title":"Exfiltrated","slides":[{"title":"S1","bullets":["leaked"]}]}`), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	manifestDir := filepath.Join(workspaceRoot, task.ID.String(), filepath.Dir(deckManifestRelPath))
	if err := os.MkdirAll(manifestDir, 0o700); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}
	manifestPath := filepath.Join(workspaceRoot, task.ID.String(), deckManifestRelPath)
	if err := os.Symlink(secretPath, manifestPath); err != nil {
		t.Fatalf("symlink manifest: %v", err)
	}

	fake.setFinalResponse("fallback text")
	fake.setStatus(controlclient.StatusFinished)

	_, summary, _, err := e.Status(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if summary != "fallback text" {
		t.Fatalf("resultSummary = %q, want the agent's own final-response text (the symlink target must never be read)", summary)
	}
	if len(pub.createCalls) != 0 || len(pub.addVersionCalls) != 0 {
		t.Fatal("expected no publish call for a manifest symlinked outside the session workspace")
	}
}

// Security review finding (HIGH): a resolve-then-reopen-by-string fix (an
// earlier version of readCapped, using filepath.EvalSymlinks then a plain
// os.Open) checks one path string and opens a second, unsynchronized,
// resolution of the same string. The agent-server is still alive at this
// point (it keeps running after its conversation finishes, and this runs
// before reap kills it), so hostile code in the sandbox can swap the file
// for a symlink in the window between the two. This does not rely on
// winning a precise race: hammering a real rename/symlink swap against real
// reads for enough iterations gives a two-step implementation a realistic
// chance of returning the secret's content at least once. os.Root's
// single-syscall resolve-and-open (readCapped's whole point now) should
// never do so, however many iterations run.
func TestReadCapped_SwapDuringOpenNeverReadsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	manifestDir := filepath.Join(root, filepath.Dir(deckManifestRelPath))
	if err := os.MkdirAll(manifestDir, 0o700); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}
	manifestPath := filepath.Join(root, deckManifestRelPath)

	outsideDir := t.TempDir()
	secretPath := filepath.Join(outsideDir, "secret.json")
	const secretMarker = "SECRET-SHOULD-NEVER-BE-READ"
	if err := os.WriteFile(secretPath, []byte(secretMarker), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	safeContent := []byte("safe content, not the secret")

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.WriteFile(manifestPath, safeContent, 0o600)
			_ = os.Remove(manifestPath)
			_ = os.Symlink(secretPath, manifestPath)
			_ = os.Remove(manifestPath)
		}
	}()
	defer func() {
		close(stop)
		<-done
	}()

	for i := 0; i < 500; i++ {
		r, err := os.OpenRoot(root)
		if err != nil {
			t.Fatalf("OpenRoot: %v", err)
		}
		data, err := readCapped(r, deckManifestRelPath, maxDeckManifestBytes)
		_ = r.Close()
		if err == nil && string(data) == secretMarker {
			t.Fatal("readCapped returned content from outside its root during a live symlink swap")
		}
	}
}

// Security review finding (MEDIUM-HIGH): a FIFO at the manifest path, with
// no file-type check and no O_NONBLOCK, hangs a plain os.Open(name)
// forever — nothing here ever opens the other end for writing — which
// leaks the goroutine, the quota slot, and the whole session, since reap()
// never runs while this is stuck. Deterministic, no race needed: mkfifo and
// call Status once.
func TestSandboxEngine_Status_RejectsFIFOManifestWithoutHanging(t *testing.T) {
	var fake *fakeAgentServer
	pub := &fakePublisher{url: "/artifacts/should-not-be-used"}
	e, workspaceRoot := newTestEngineWithPublisher(t, &fake, pub)

	task := knowledgeWorkTask()
	sessionRef, err := e.Launch(context.Background(), task)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	manifestDir := filepath.Join(workspaceRoot, task.ID.String(), filepath.Dir(deckManifestRelPath))
	if err := os.MkdirAll(manifestDir, 0o700); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}
	manifestPath := filepath.Join(workspaceRoot, task.ID.String(), deckManifestRelPath)
	if err := syscall.Mkfifo(manifestPath, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	fake.setFinalResponse("fallback text")
	fake.setStatus(controlclient.StatusFinished)

	type result struct {
		summary string
		err     error
	}
	done := make(chan result, 1)
	go func() {
		_, summary, _, statusErr := e.Status(context.Background(), sessionRef)
		done <- result{summary, statusErr}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Status: %v", r.err)
		}
		if r.summary != "fallback text" {
			t.Fatalf("resultSummary = %q, want the agent's own final-response text", r.summary)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Status did not return within 2s: a FIFO at the manifest path hung the open, exactly the finding this test guards")
	}
	if len(pub.createCalls) != 0 {
		t.Fatal("expected no publish call for a FIFO manifest")
	}
}

// Go review finding: two concurrent Status calls for the same session each
// independently observed the terminal execution status, each fetched the
// final response and called publishDeckArtifact, and each returned its own
// locally computed result — two Create calls, two different real artifact
// URLs for one task, and whichever one lost the race to cache in
// sess.terminal was silently orphaned. This forces the exact interleaving:
// the first call is parked inside Create (holding sess.finishMu) when the
// second one starts, so the second is guaranteed to block on that lock
// rather than possibly running to completion first.
func TestSandboxEngine_Status_ConcurrentCallsPublishExactlyOnce(t *testing.T) {
	var fake *fakeAgentServer
	pub := &fakePublisher{
		url:     "/artifacts/only-once",
		started: make(chan struct{}, 1),
		gate:    make(chan struct{}),
	}
	e, workspaceRoot := newTestEngineWithPublisher(t, &fake, pub)

	task := knowledgeWorkTask()
	sessionRef, err := e.Launch(context.Background(), task)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	writeDeckManifest(t, workspaceRoot, task.ID, `{"title":"Race","slides":[{"title":"S1","bullets":["x"]}]}`)
	fake.setFinalResponse("fallback text")
	fake.setStatus(controlclient.StatusFinished)

	type result struct {
		summary string
		err     error
	}
	results := make(chan result, 2)
	go func() {
		_, summary, _, statusErr := e.Status(context.Background(), sessionRef)
		results <- result{summary, statusErr}
	}()

	// Wait until the first call is actually inside Create (so it already
	// holds sess.finishMu) before starting the second.
	<-pub.started

	go func() {
		_, summary, _, statusErr := e.Status(context.Background(), sessionRef)
		results <- result{summary, statusErr}
	}()

	// No signal exists for "a goroutine is now blocked on a mutex"; this
	// short, one-time sleep is deliberate margin for the second goroutine to
	// actually reach and block on finishMu.Lock() before the gate opens, not
	// a poll loop.
	time.Sleep(20 * time.Millisecond)
	close(pub.gate)

	first := <-results
	second := <-results
	for _, r := range []result{first, second} {
		if r.err != nil {
			t.Fatalf("Status: %v", r.err)
		}
		if r.summary != pub.url {
			t.Fatalf("resultSummary = %q, want %q", r.summary, pub.url)
		}
	}
	if len(pub.createCalls) != 1 {
		t.Fatalf("expected exactly one Create call across two concurrent Status calls, got %d", len(pub.createCalls))
	}
}

// A Cancel that lands while a knowledge-work-pack task is finishing must not
// race with the publish. reap clears sess.bearerJWT under e.mu, and Cancel
// reaches reap without ever taking sess.finishMu, so a publish that read that
// field unlocked races with it. CI runs `go test -short -race`, so this fails
// the build rather than corrupting a publish in production.
//
// The window is opened deliberately, and where it actually matters. The
// engine is held inside the final-response fetch, which is the last step
// before publishDeckArtifact reads the JWT, and Cancel is launched from
// there. Gating on the publisher instead would be useless: Create runs after
// the read, so waiting for it orders the read before the cancel and destroys
// the very interleaving under test. That version of this test passed against
// a deliberately unsynchronized read, which is how the ordering bug in it was
// found.
func TestSandboxEngine_Status_CancelDuringPublishIsRaceFree(t *testing.T) {
	var fake *fakeAgentServer
	pub := &fakePublisher{url: "/artifacts/cancel-race"}
	e, workspaceRoot := newTestEngineWithPublisher(t, &fake, pub)

	task := knowledgeWorkTask()
	sessionRef, err := e.Launch(context.Background(), task)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	writeDeckManifest(t, workspaceRoot, task.ID, `{"title":"Cancel","slides":[{"title":"S1","bullets":["x"]}]}`)
	fake.setFinalResponse("fallback text")

	finalStarted, finalGate := make(chan struct{}, 1), make(chan struct{})
	fake.setFinalGate(finalStarted, finalGate)
	fake.setStatus(controlclient.StatusFinished)

	done := make(chan error, 1)
	go func() {
		_, _, _, statusErr := e.Status(context.Background(), sessionRef)
		done <- statusErr
	}()

	// Parked inside the final-response fetch, so the bearer JWT has not been
	// read yet and the cancel below is genuinely concurrent with that read.
	<-finalStarted

	cancelled := make(chan error, 1)
	go func() { cancelled <- e.Cancel(context.Background(), sessionRef) }()
	close(finalGate)

	if cancelErr := <-cancelled; cancelErr != nil {
		t.Fatalf("Cancel: %v", cancelErr)
	}
	if statusErr := <-done; statusErr != nil {
		t.Fatalf("Status: %v", statusErr)
	}

	// Whether the publish or the reap wins is a legitimate race in outcome
	// and this deliberately does not pin it. What must hold either way is
	// that no Create ever ran with a half-cleared credential.
	pub.mu.Lock()
	defer pub.mu.Unlock()
	for _, call := range pub.createCalls {
		if call.bearerJWT == "" {
			t.Fatal("Create ran with a blank bearer JWT, so reap cleared it mid-publish")
		}
	}
}

// A nil Publisher (Config's zero value, and every SandboxEngine built before
// this field existed) must behave exactly as before: no publish attempt at
// all, regardless of pack or manifest.
func TestSandboxEngine_Status_NilPublisherNeverPublishes(t *testing.T) {
	var fake *fakeAgentServer
	e := newTestEngine(t, &fake) // Config.Publisher left nil

	task := knowledgeWorkTask()
	sessionRef, err := e.Launch(context.Background(), task)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	writeDeckManifest(t, e.cfg.WorkspaceRoot, task.ID, `{"title":"No Publisher","slides":[{"title":"S1","bullets":["x"]}]}`)

	fake.setFinalResponse("fallback text")
	fake.setStatus(controlclient.StatusFinished)

	_, summary, _, err := e.Status(context.Background(), sessionRef)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if summary != "fallback text" {
		t.Fatalf("resultSummary = %q, want the agent's own final-response text", summary)
	}
}
