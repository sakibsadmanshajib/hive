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

func newTestEngineWithQuota(t *testing.T, captured **fakeAgentServer, tenantConcurrency, userConcurrency int) *SandboxEngine {
	t.Helper()
	cfg := Config{
		QuotaTenantConcurrency: tenantConcurrency,
		QuotaUserConcurrency:   userConcurrency,

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
}

func (f *fakePublisher) Create(_ context.Context, bearerJWT, name, html string) (artifactsclient.Artifact, error) {
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
		PacksDir:      t.TempDir(),
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
