package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/controlclient"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/sandbox"
)

// fakeAgentServer stands in for the real OpenHands agent-server (Python,
// unavailable in this test environment) behind the control socket, so
// SandboxEngine's control-channel wiring is exercised without Apptainer.
// It also implements process, acting as the fake sandbox subprocess's
// handle: Kill tears down the fake server the same way killing the real
// sandbox process would take the agent-server down with it.
type fakeAgentServer struct {
	mu              sync.Mutex
	conversationID  uuid.UUID
	executionStatus controlclient.ExecutionStatus
	finalResponse   string
	ran             bool
	interrupted     bool
	killed          bool
	startReq        controlclient.StartConversationRequest

	listener net.Listener
	srv      *http.Server
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
		_ = json.NewDecoder(r.Body).Decode(&f.startReq)
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
	mux.HandleFunc(convoPrefix+"/agent_final_response", func(w http.ResponseWriter, r *http.Request) {
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

func (f *fakeAgentServer) setFinalResponse(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finalResponse = s
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
	cfg := Config{
		SIFPath:       "/fake/agent-server.sif",
		PacksDir:      t.TempDir(),
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

	if _, _, _, err := e.Status(context.Background(), sessionRef); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("expected session to be forgotten after Cancel, got %v", err)
	}
}
