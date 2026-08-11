// Package engine composes apps/agent-engine/internal/sandbox (the Apptainer
// launcher) and apps/agent-engine/internal/controlclient (the host<->agent-
// server control channel, issue #305) into one Launch/Status/Cancel session
// lifecycle: SandboxEngine.Launch starts a sandbox, waits for its control
// socket to come up, and submits the task; Status polls the agent-server's
// conversation state and maps it onto the queued/running/succeeded/failed/
// cancelled vocabulary apps/control-plane/internal/agenttask's
// SYNC_CONTRACT.md defines; Cancel interrupts the conversation and
// terminates the sandbox process.
//
// This package does not implement agenttask.Engine directly, and cannot:
// agenttask lives under apps/control-plane/internal (a different Go
// module), and Go's internal-package visibility does not cross module
// boundaries — apps/agent-engine/internal/egressclient documents the exact
// same limitation and works around it by redeclaring the one constant it
// needs rather than importing across the boundary. SandboxEngine's Task and
// Status types below deliberately mirror agenttask.Task and agenttask.Status
// field-for-field for the same reason. Once issue #311's agenttask package
// merges, control-plane's own Engine adapter wraps a *SandboxEngine: it
// translates agenttask.Task -> engine.Task on the way in and the Launch
// return value plus subsequent Status polls back onto agenttask.Status /
// EngineSessionRef / ResultSummaryRef / ErrorMessage on the way out. That
// adapter is a thin (~20 line) translation layer, not duplicated business
// logic — the actual launch/poll/cancel logic lives here, once.
//
// Known gap this package does not attempt to close: agenttask.Task (as of
// issue #311) carries no prompt or LLM/agent-profile reference, only an ID
// and a Pack. Launch therefore starts every conversation against
// Config.AgentProfileID, one profile shared by the whole engine instance
// (ponytail: no per-task profile selection exists to wire yet; add a
// per-task profile lookup once agenttask or a sibling table carries one).
package engine

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/controlclient"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/egressproxy"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/quota"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/sandbox"
)

// Task is the minimal shape SandboxEngine needs from a queued task. It
// mirrors apps/control-plane/internal/agenttask.Task's ID/TenantID/UserID/
// Pack fields (see the package doc for why it cannot just be that type).
type Task struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	UserID       uuid.UUID
	Pack         string
	Instructions string // free-text prompt/goal; empty means no initial message is sent
}

// Status mirrors apps/control-plane/internal/agenttask.Status's values
// (SYNC_CONTRACT.md's state machine).
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// ErrUnknownSession is returned by Status/Cancel when sessionRef does not
// match a session this SandboxEngine instance launched. Sessions are held
// in memory only (ponytail: no disk-persisted session registry — a process
// restart loses the ability to Status/Cancel an in-flight session, but the
// sandbox process is a child of this one and would not survive the restart
// either, so persistence buys nothing until sandbox processes outlive this
// process; add a registry if/when that changes).
var ErrUnknownSession = fmt.Errorf("engine: unknown session reference")

// Config is the per-process configuration shared by every session a
// SandboxEngine launches.
type Config struct {
	SIFPath       string // built agent-server SIF (HIVE_AGENT_SIF_PATH)
	PacksDir      string // parent dir of "<pack>/" AGENTS.md config dirs, e.g. "packs"
	WorkspaceRoot string // parent dir; each session gets WorkspaceRoot/<task-id> as its /workspace bind mount
	RunDir        string // parent dir for per-session egress+control socket dirs

	// ResolveEgressHosts resolves the effective egress allowlist for a
	// tenant/user (apps/agent-engine/internal/egressclient.Client.Effective
	// has this exact signature). A returned error fails the launch outright
	// rather than launching with an unknown policy.
	ResolveEgressHosts func(ctx context.Context, tenantID, userID uuid.UUID) ([]string, error)

	// AgentProfileID is the server-side agent profile every launched
	// conversation uses (see the package doc's "known gap" section). Ignored
	// when LLMModel is set.
	AgentProfileID uuid.UUID

	// LLMModel, LLMBaseURL and LLMAPIKey describe the OpenAI-compatible
	// endpoint launched conversations call. When LLMModel is set, Launch
	// sends an inline agent_settings payload instead of AgentProfileID.
	//
	// This is the only shape that works on a sandbox launched with
	// --containall: the agent-server resolves an agent_profile_id against a
	// profile store on its own filesystem, and that filesystem is a fresh
	// empty container every session, so no stored profile can ever exist
	// there. The key reaches the agent-server over the per-session Unix
	// control socket only.
	LLMModel   string
	LLMBaseURL string
	LLMAPIKey  string

	// SessionAPIKey, when set, is both passed to the sandbox
	// (sandbox.LaunchConfig.SessionAPIKey, actually enforced server-side)
	// and sent by the control client as controlclient.SessionAPIKeyHeader.
	// Optional: empty means the control socket's filesystem permissions are
	// the only trust boundary.
	SessionAPIKey string

	// ControlReadyTimeout bounds how long Launch waits for the in-SIF shim
	// to create its control socket. Defaults to 30s.
	ControlReadyTimeout time.Duration

	// QuotaTenantConcurrency and QuotaUserConcurrency cap how many sessions
	// one tenant and one user may run at once (issue #308). Non-positive
	// values fall back to the defaults below.
	QuotaTenantConcurrency int
	QuotaUserConcurrency   int

	// MemoryLimit, CPULimit and PidsLimit bound what each individual session
	// may consume; see sandbox.LaunchConfig's fields of the same name.
	MemoryLimit string
	CPULimit    string
	PidsLimit   int
}

func (c Config) withDefaults() Config {
	if c.ControlReadyTimeout <= 0 {
		c.ControlReadyTimeout = 30 * time.Second
	}
	if c.QuotaTenantConcurrency <= 0 {
		c.QuotaTenantConcurrency = 4
	}
	if c.QuotaUserConcurrency <= 0 {
		c.QuotaUserConcurrency = 2
	}
	// Sized for a coding-pack session: an arbitrary build plus the headless
	// Chromium the browser tool drives.
	if c.MemoryLimit == "" {
		c.MemoryLimit = "4G"
	}
	if c.CPULimit == "" {
		c.CPULimit = "2"
	}
	if c.PidsLimit <= 0 {
		c.PidsLimit = 512
	}
	return c
}

// process abstracts the running sandbox subprocess so tests can substitute
// a fake that never actually execs apptainer.
type process interface {
	Kill() error
}

type osProcess struct{ cmd *exec.Cmd }

func (p *osProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

// realStart execs argv (sandbox.BuildArgv's output) as a background
// subprocess.
func realStart(argv []string) (process, error) {
	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- argv is built entirely by sandbox.BuildArgv from validated, non-shell-interpreted config
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("engine: start sandbox process: %w", err)
	}
	return &osProcess{cmd: cmd}, nil
}

// terminalOutcome is a reaped session's final answer, replayed by Status
// instead of dialling a control socket whose sandbox is already gone.
type terminalOutcome struct {
	status        Status
	resultSummary string
	errMessage    string
}

type session struct {
	conversationID uuid.UUID
	client         *controlclient.Client
	proc           process
	proxySrv       *http.Server
	proxyListener  net.Listener
	sessionDir     string // removed when reaped; holds only Unix sockets
	workingDir     string // removed when reaped; the sandbox's /workspace bind mount
	release        func() // frees this session's quota slot; safe to call repeatedly

	// reaped and terminal are guarded by SandboxEngine.mu.
	reaped   bool
	terminal *terminalOutcome
}

// SandboxEngine launches, polls, and cancels agent-engine sandbox sessions.
// The zero value is not usable; construct with New.
type SandboxEngine struct {
	cfg   Config
	start func(argv []string) (process, error)
	q     *quota.Manager

	mu sync.Mutex
	// ponytail: a reaped session keeps a small terminal record here instead
	// of being deleted, so the map grows by one struct per completed task and
	// only a process restart clears it. Add eviction if one process ever runs
	// enough tasks for that to matter.
	sessions map[string]*session
}

// New constructs a SandboxEngine from cfg.
func New(cfg Config) *SandboxEngine {
	cfg = cfg.withDefaults()
	q, err := quota.New(quota.Limits{
		TenantConcurrency: cfg.QuotaTenantConcurrency,
		UserConcurrency:   cfg.QuotaUserConcurrency,
	})
	if err != nil {
		// Unreachable: withDefaults replaces every non-positive limit.
		panic(err)
	}
	return &SandboxEngine{
		cfg:      cfg,
		start:    realStart,
		q:        q,
		sessions: make(map[string]*session),
	}
}

// freePort asks the OS for an ephemeral TCP port and immediately releases
// it, for use as the agent-server's --port. Racy in theory (another process
// could grab it first) but standard practice for this and acceptable here:
// the window is a few milliseconds before apptainer binds it.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("engine: allocate free port: %w", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Launch starts a sandbox session for t: stands up the egress proxy,
// builds and starts the Apptainer sandbox (apps/agent-engine/internal/
// sandbox), waits for the in-SIF shim's control socket to come up, then
// submits and runs the conversation over that socket. The returned
// sessionRef (the agent-server's conversation id) is what Status/Cancel
// take.
func (e *SandboxEngine) Launch(ctx context.Context, t Task) (sessionRef string, err error) {
	if t.Pack == "" {
		return "", fmt.Errorf("engine: Task.Pack is required")
	}

	release, err := e.q.Acquire(t.TenantID, t.UserID)
	if err != nil {
		return "", err
	}
	// succeeded flips true on the return at the very end of this function; it
	// gates this cleanup and the resource cleanup registered further down.
	succeeded := false
	defer func() {
		if !succeeded {
			release()
		}
	}()

	allowedHosts, err := e.cfg.ResolveEgressHosts(ctx, t.TenantID, t.UserID)
	if err != nil {
		return "", fmt.Errorf("engine: resolve egress policy, refusing to launch: %w", err)
	}

	// sessionDir deliberately does NOT embed t.ID (a 36-char UUID): it only
	// ever holds Unix domain sockets, whose sun_path is capped at ~108
	// bytes on Linux, so os.MkdirTemp's short auto-generated name is used
	// instead of anything human-readable. workingDir has no such
	// constraint (a regular bind-mounted directory, not a socket path) and
	// keeps the task ID for operator readability.
	sessionDir, err := os.MkdirTemp(e.cfg.RunDir, "")
	if err != nil {
		return "", fmt.Errorf("engine: create session directory under %s: %w", e.cfg.RunDir, err)
	}
	controlDir := filepath.Join(sessionDir, "c")
	workingDir := filepath.Join(e.cfg.WorkspaceRoot, t.ID.String())

	// Single deferred cleanup for every failure branch below: closes
	// whatever got started so far and removes both directories.
	var (
		proxySrv *http.Server
		proc     process
	)
	defer func() {
		if succeeded {
			return
		}
		if proc != nil {
			_ = proc.Kill()
		}
		if proxySrv != nil {
			_ = proxySrv.Close()
		}
		_ = os.RemoveAll(sessionDir)
		_ = os.RemoveAll(workingDir)
	}()

	for _, dir := range []string{controlDir, workingDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("engine: create session directory %s: %w", dir, err)
		}
	}

	egressSocketPath := filepath.Join(sessionDir, "e.sock")
	proxyListener, err := net.Listen("unix", egressSocketPath)
	if err != nil {
		return "", fmt.Errorf("engine: start egress proxy listener: %w", err)
	}
	proxySrv = &http.Server{Handler: egressproxy.New(allowedHosts)}
	go func() { _ = proxySrv.Serve(proxyListener) }()

	hostPort, err := freePort()
	if err != nil {
		return "", err
	}

	lc := sandbox.LaunchConfig{
		TenantID: t.TenantID,
		UserID:   t.UserID,
		Pack: sandbox.Pack{
			Name:       t.Pack,
			ConfigDir:  filepath.Join(e.cfg.PacksDir, t.Pack),
			WorkingDir: workingDir,
		},
		SIFPath:          e.cfg.SIFPath,
		HostPort:         hostPort,
		ProxySocketPath:  egressSocketPath,
		ControlSocketDir: controlDir,
		SessionAPIKey:    e.cfg.SessionAPIKey,
		MemoryLimit:      e.cfg.MemoryLimit,
		CPULimit:         e.cfg.CPULimit,
		PidsLimit:        e.cfg.PidsLimit,
	}

	argv, err := sandbox.BuildArgv(lc)
	if err != nil {
		return "", fmt.Errorf("engine: build sandbox argv: %w", err)
	}

	proc, err = e.start(argv)
	if err != nil {
		return "", err
	}

	readyCtx, cancel := context.WithTimeout(ctx, e.cfg.ControlReadyTimeout)
	defer cancel()
	controlSocketPath := sandbox.ControlSocketPath(lc)
	if err := controlclient.WaitReady(readyCtx, controlSocketPath); err != nil {
		return "", err
	}

	client := controlclient.New(controlSocketPath, e.cfg.SessionAPIKey)
	req := controlclient.StartConversationRequest{
		Workspace: controlclient.LocalWorkspace("/workspace"),
	}
	if e.cfg.LLMModel != "" {
		req.AgentSettings = &controlclient.AgentSettings{
			AgentKind: "openhands",
			LLM: controlclient.LLMSettings{
				Model:   e.cfg.LLMModel,
				BaseURL: e.cfg.LLMBaseURL,
				APIKey:  e.cfg.LLMAPIKey,
				UsageID: "hive-agent",
			},
		}
	} else {
		profileID := e.cfg.AgentProfileID
		req.AgentProfileID = &profileID
	}
	if t.Instructions != "" {
		req.InitialMessage = &controlclient.SendMessageRequest{
			Role:    "user",
			Content: []controlclient.TextContent{controlclient.Text(t.Instructions)},
		}
	}
	convo, err := client.StartConversation(ctx, req)
	if err != nil {
		return "", fmt.Errorf("engine: start conversation: %w", err)
	}
	if err := client.Run(ctx, convo.ID); err != nil {
		return "", fmt.Errorf("engine: run conversation: %w", err)
	}

	sess := &session{
		conversationID: convo.ID,
		client:         client,
		proc:           proc,
		proxySrv:       proxySrv,
		proxyListener:  proxyListener,
		sessionDir:     sessionDir,
		workingDir:     workingDir,
		release:        release,
	}
	e.mu.Lock()
	e.sessions[convo.ID.String()] = sess
	e.mu.Unlock()

	succeeded = true
	return convo.ID.String(), nil
}

// Status polls sessionRef's current state and maps it onto the
// queued/running/succeeded/failed/cancelled vocabulary
// apps/control-plane/internal/agenttask's SYNC_CONTRACT.md defines.
// resultSummary is populated only when status is StatusSucceeded;
// errMessage only when StatusFailed.
func (e *SandboxEngine) Status(ctx context.Context, sessionRef string) (status Status, resultSummary, errMessage string, err error) {
	sess, id, err := e.lookup(sessionRef)
	if err != nil {
		return "", "", "", err
	}

	e.mu.Lock()
	replay := sess.terminal
	e.mu.Unlock()
	if replay != nil {
		return replay.status, replay.resultSummary, replay.errMessage, nil
	}

	info, err := sess.client.GetConversation(ctx, id)
	if err != nil {
		return "", "", "", fmt.Errorf("engine: get conversation: %w", err)
	}

	switch info.ExecutionStatus {
	case controlclient.StatusFinished:
		summary, err := sess.client.FinalResponse(ctx, id)
		if err != nil {
			return "", "", "", fmt.Errorf("engine: get final response: %w", err)
		}
		status, resultSummary = StatusSucceeded, summary
	case controlclient.StatusErrored, controlclient.StatusStuck:
		status, errMessage = StatusFailed, fmt.Sprintf("agent-server execution_status=%s", info.ExecutionStatus)
	case controlclient.StatusPaused, controlclient.StatusDeleting:
		// paused only ever happens via this package's own Cancel today
		// (ponytail: an externally-triggered pause would also read as
		// cancelled here — no other component pauses a conversation yet).
		status = StatusCancelled
	case controlclient.StatusIdle:
		return StatusQueued, "", "", nil
	default: // running, waiting_for_confirmation, or a future value
		return StatusRunning, "", "", nil
	}

	// Terminal. Nothing else ever frees this session: the agent-server is a
	// server and keeps running after its conversation finishes, and
	// apps/control-plane/internal/agenttask's poller only records the status,
	// it never calls Cancel. Reaping here is what stops completed tasks from
	// leaking their sandbox process, directories and quota slot.
	_ = e.reap(sess, &terminalOutcome{status: status, resultSummary: resultSummary, errMessage: errMessage})
	return status, resultSummary, errMessage, nil
}

// reap frees everything sess holds and records outcome for later Status
// calls to replay. Idempotent: only the first call per session does the work.
// The returned error is the sandbox process kill failure, which Cancel
// surfaces to its caller and Status has nothing useful to do with.
func (e *SandboxEngine) reap(sess *session, outcome *terminalOutcome) error {
	e.mu.Lock()
	if sess.reaped {
		e.mu.Unlock()
		return nil
	}
	sess.reaped = true
	sess.terminal = outcome
	e.mu.Unlock()

	killErr := sess.proc.Kill()
	_ = sess.proxySrv.Close()
	_ = os.RemoveAll(sess.sessionDir)
	_ = os.RemoveAll(sess.workingDir)
	sess.release()
	return killErr
}

// Cancel interrupts sessionRef's conversation and terminates its sandbox
// process, freeing the session's resources.
func (e *SandboxEngine) Cancel(ctx context.Context, sessionRef string) error {
	sess, id, err := e.lookup(sessionRef)
	if err != nil {
		return err
	}

	interruptErr := sess.client.Interrupt(ctx, id)
	// The session entry stays in the map holding its terminal outcome rather
	// than being deleted: agenttask's poller retries Status whenever its own
	// Transition call failed, and an unknown-session error there would leave
	// the task active forever.
	killErr := e.reap(sess, &terminalOutcome{status: StatusCancelled})

	if interruptErr != nil {
		return fmt.Errorf("engine: interrupt conversation: %w", interruptErr)
	}
	if killErr != nil {
		return fmt.Errorf("engine: kill sandbox process: %w", killErr)
	}
	return nil
}

func (e *SandboxEngine) lookup(sessionRef string) (*session, uuid.UUID, error) {
	id, err := uuid.Parse(sessionRef)
	if err != nil {
		return nil, uuid.Nil, fmt.Errorf("%w: %s", ErrUnknownSession, sessionRef)
	}
	e.mu.Lock()
	sess, ok := e.sessions[sessionRef]
	e.mu.Unlock()
	if !ok {
		return nil, uuid.Nil, fmt.Errorf("%w: %s", ErrUnknownSession, sessionRef)
	}
	return sess, id, nil
}
