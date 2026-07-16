// Package sandbox constructs the Apptainer rootless launch command for a
// coding-pack or knowledge-work-pack agent-engine session (issue #305/#308).
// Every invocation this package builds carries the enforced defaults proven
// necessary by security spike #307 and by team-lead security review of this
// package:
//
//   - --pid and --containall: Apptainer's default shares the host PID
//     namespace (spike implementation condition 1) — the upstream
//     ApptainerWorkspace this package wraps (vendor/openhands) does not
//     pass either by default.
//   - --net --network none: the sandbox gets an isolated network namespace
//     with only a loopback interface, no route to the host network at all.
//     An earlier version of this package bound egress only by setting
//     HTTP_PROXY/HTTPS_PROXY — advisory only, since a raw socket or any
//     proxy-unaware library inside the sandbox could dial out directly.
//     With --network none there is no interface to dial out on except
//     loopback, so the only way traffic leaves the sandbox at all is via
//     the one relay path below. "none" is the only network configuration
//     Apptainer permits non-privileged (rootless) users to request
//     (https://apptainer.org/docs/user/main/networking.html, verified
//     2026-07-16); no CNI plugin or setuid installation is required.
//   - the one relay path: a per-session egressproxy.Proxy listens on a Unix
//     socket on the host (network namespaces don't affect bind mounts,
//     which are a filesystem operation), bind-mounted into the sandbox. A
//     socat shim baked into the SIF (deploy/apptainer/agent-engine.def)
//     forwards a fixed loopback port, reachable only from inside the
//     sandbox's own isolated netns, to that bind-mounted socket. HTTP_PROXY
//     and HTTPS_PROXY point at that loopback port.
//
// Host <-> agent-server control channel (issue #305, closing the Wave 3 gap
// the paragraph above used to describe): with --network none the host
// cannot reach the agent-server's own --host/--port over TCP, since that
// path required the network-namespace sharing this package deliberately
// removes. The control channel mirrors the egress relay but in the opposite
// direction: LaunchConfig.ControlSocketDir is a host directory, empty at
// launch time and bind-mounted (read-write — see ControlSocketContainerDir's
// doc comment for why it cannot be read-only) into the sandbox. The in-SIF
// shim (deploy/apptainer/agent-engine.def) creates its own listening Unix
// socket inside that mounted directory, forwarding each connection to
// 127.0.0.1:<HostPort> (the agent-server's own loopback bind) inside the
// sandbox's netns. Because the mount is a directory, not a single
// pre-existing file, the socket file the shim creates becomes visible at the
// mirrored host path once the shim starts, and the host can dial it
// directly with no route through the egress proxy at all. See
// apps/agent-engine/internal/controlclient for the host-side client.
//
// BuildArgv also refuses to launch if any bind mount or working directory in
// the constructed command references the Docker socket (spike rows 8/9,
// issue #307).
package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// ProxyShimPort is the fixed loopback port, inside the sandbox's own
// isolated network namespace, that the SIF's socat shim listens on and
// forwards to the bind-mounted egress-proxy Unix socket. It only needs to be
// unique within that one sandbox's own netns, so a fixed value is fine: no
// two sandbox sessions ever share a network namespace.
const ProxyShimPort = 3128

// ProxySocketContainerPath is the fixed path, inside the sandbox, that the
// host's per-session egressproxy.Proxy Unix socket is bind-mounted to.
const ProxySocketContainerPath = "/opt/hive/egress.sock"

// ControlSocketContainerDir is the fixed path, inside the sandbox, that
// LaunchConfig.ControlSocketDir is bind-mounted to. Unlike
// ProxySocketContainerPath (a single socket file the host creates before
// launch), this bind mount is a directory and must stay read-write: the
// in-SIF shim creates its listening socket file inside it only after the
// sandbox starts, and creating a new directory entry needs write access to
// the directory itself. ValidateSecurityDefaults still asserts the mount is
// scoped to exactly this one directory pair — a read-write directory bind
// is only as safe as what else lives in it, and nothing else is bind-mounted
// there.
const ControlSocketContainerDir = "/opt/hive/control"

// ControlSocketFileName is the file name the in-SIF shim binds its listening
// Unix socket to, inside ControlSocketContainerDir. The mirrored host path
// (for apps/agent-engine/internal/controlclient to dial) is
// filepath.Join(cfg.ControlSocketDir, ControlSocketFileName); see
// ControlSocketPath.
const ControlSocketFileName = "agent.sock"

// ControlSocketPath returns the host-side path of the control channel's Unix
// socket for cfg: the path apps/agent-engine/internal/controlclient dials
// once the in-SIF shim has created it. The file does not exist until the
// sandbox process has started and the shim has run.
func ControlSocketPath(cfg LaunchConfig) string {
	return filepath.Join(cfg.ControlSocketDir, ControlSocketFileName)
}

// Pack identifies which microagent pack config the sandbox mounts. Coding
// and knowledge-work packs share the identical sandbox trust tier — both may
// run arbitrary shell, build, and test commands inside the container
// (blueprint Step 2.2); Pack only selects which pack config directory is
// bind-mounted, it never changes the security posture.
type Pack struct {
	Name       string
	ConfigDir  string // host path to the pack's AGENTS.md config, bind-mounted read-only
	WorkingDir string // host path bind-mounted read-write as the agent's /workspace
}

// LaunchConfig is everything BuildArgv needs to construct one sandbox
// invocation.
type LaunchConfig struct {
	TenantID        uuid.UUID
	UserID          uuid.UUID
	Pack            Pack
	SIFPath         string
	HostPort        int    // passed to the agent-server's own --host/--port; reachable from the host only via ControlSocketDir, see package doc
	ProxySocketPath string // host path of the per-session egressproxy.Proxy's Unix socket listener

	// ControlSocketDir is a host directory, created empty by the caller
	// before launch, bind-mounted read-write into the sandbox at
	// ControlSocketContainerDir. See the package doc and
	// apps/agent-engine/internal/controlclient.
	ControlSocketDir string

	// SessionAPIKey, when set, is passed to the agent-server as the
	// SESSION_API_KEY env var (vendor/openhands/openhands-agent-server's
	// openhands/agent_server/config.py V0_SESSION_API_KEY_ENV), so it
	// actually enforces apps/agent-engine/internal/controlclient's
	// X-Session-API-Key header instead of that header being a no-op.
	// Optional: empty means the agent-server enforces no session key at
	// all — the control socket's filesystem permissions are the only trust
	// boundary in that case.
	SessionAPIKey string

	// MCPConfigPath is the host path of the generated OpenHands-native
	// {"mcpServers": {...}} JSON document (issue #309, blueprint Step 2.3;
	// see apps/agent-engine/internal/marketplaceclient.BuildConfig and
	// cmd/agent-engine/main.go). Optional: empty means no marketplace MCP
	// servers are bind-mounted for this session. When set, it is bind-mounted
	// read-only at MCPConfigContainerPath.
	MCPConfigPath string
}

// MCPConfigContainerPath is the fixed path, inside the sandbox, that
// LaunchConfig.MCPConfigPath is bind-mounted to when set.
const MCPConfigContainerPath = "/opt/hive/mcp_config.json"

var (
	ErrMissingSIFPath          = errors.New("sandbox: SIFPath is required")
	ErrMissingProxySocketPath  = errors.New("sandbox: ProxySocketPath is required")
	ErrMissingControlSocketDir = errors.New("sandbox: ControlSocketDir is required")
	ErrMissingConfigDir        = errors.New("sandbox: Pack.ConfigDir is required")
	ErrMissingWorkingDir       = errors.New("sandbox: Pack.WorkingDir is required")
	ErrInvalidHostPort         = errors.New("sandbox: HostPort must be positive")
	ErrNilTenant               = errors.New("sandbox: TenantID must not be uuid.Nil")
	ErrNilUser                 = errors.New("sandbox: UserID must not be uuid.Nil")

	// ErrDockerSocketReferenced is returned when a constructed command
	// references the Docker socket by path or well-known env var — this
	// must never be reachable from an agent process (spike #307 rows 8/9).
	ErrDockerSocketReferenced = errors.New("sandbox: refusing to launch: command references the Docker socket")
)

func (c LaunchConfig) validate() error {
	switch {
	case c.TenantID == uuid.Nil:
		return ErrNilTenant
	case c.UserID == uuid.Nil:
		return ErrNilUser
	case c.SIFPath == "":
		return ErrMissingSIFPath
	case c.ProxySocketPath == "":
		return ErrMissingProxySocketPath
	case c.ControlSocketDir == "":
		return ErrMissingControlSocketDir
	case c.Pack.ConfigDir == "":
		return ErrMissingConfigDir
	case c.Pack.WorkingDir == "":
		return ErrMissingWorkingDir
	case c.HostPort <= 0:
		return ErrInvalidHostPort
	}
	return nil
}

// RequiredSecurityFlags are the standalone flags security spike #307 and
// team-lead security review proved must always be present on every
// Apptainer invocation this package (or any future patched vendor copy of
// the upstream ApptainerWorkspace) constructs. "--network none" is checked
// separately (ValidateSecurityDefaults) since it is a flag+value pair, not
// a standalone token.
var RequiredSecurityFlags = []string{"--pid", "--containall", "--net"}

// ValidateSecurityDefaults reports an error if argv is missing any flag in
// RequiredSecurityFlags, or is missing the adjacent "--network" "none" pair.
func ValidateSecurityDefaults(argv []string) error {
	present := make(map[string]bool, len(argv))
	for _, a := range argv {
		present[a] = true
	}
	var missing []string
	for _, f := range RequiredSecurityFlags {
		if !present[f] {
			missing = append(missing, f)
		}
	}
	if !hasAdjacentPair(argv, "--network", "none") {
		missing = append(missing, `--network none`)
	}
	if !hasBindTargetingContainerPath(argv, ControlSocketContainerDir) {
		missing = append(missing, "--bind <dir>:"+ControlSocketContainerDir)
	}
	if len(missing) > 0 {
		return fmt.Errorf("sandbox: missing required security flags: %s", strings.Join(missing, ", "))
	}
	return nil
}

// hasAdjacentPair reports whether argv contains flag immediately followed
// by value at some position i, i+1.
func hasAdjacentPair(argv []string, flag, value string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == value {
			return true
		}
	}
	return false
}

// hasBindTargetingContainerPath reports whether argv contains a --bind
// <host>:<containerPath>[:opts] pair whose container-side target is exactly
// containerPath. Used to assert the control-socket bind mount is present
// without hard-coding the host-side directory, which varies per launch.
func hasBindTargetingContainerPath(argv []string, containerPath string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "--bind" {
			continue
		}
		parts := strings.SplitN(argv[i+1], ":", 3)
		if len(parts) >= 2 && parts[1] == containerPath {
			return true
		}
	}
	return false
}

const dockerSocketPath = "/var/run/docker.sock"

// containsDockerSocketReference reports whether any of the given strings
// mentions the Docker socket path or the DOCKER_HOST env var, case
// insensitive.
func containsDockerSocketReference(parts []string) bool {
	for _, p := range parts {
		lower := strings.ToLower(p)
		if strings.Contains(lower, "docker.sock") || strings.Contains(lower, "docker_host") {
			return true
		}
	}
	return false
}

// BuildArgv constructs the `apptainer run` argv for cfg. Every returned argv
// unconditionally includes --pid, --containall, and --net --network none,
// and bind-mounts cfg.ProxySocketPath as the sandbox's only relay to the
// outside world: HTTP_PROXY/HTTPS_PROXY point at the SIF's socat shim
// (ProxyShimPort), which forwards to that bind-mounted socket. BuildArgv
// refuses (ErrDockerSocketReferenced) if the resulting command would
// reference the Docker socket in any form.
func BuildArgv(cfg LaunchConfig) (argv []string, err error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", ProxyShimPort)

	argv = []string{
		"apptainer", "run",
		"--pid",
		"--containall",
		"--net",
		"--network", "none",
		"--no-mount", "hostfs",
		"--no-mount", "bind-paths",
		"--env", "HTTP_PROXY=" + proxyURL,
		"--env", "HTTPS_PROXY=" + proxyURL,
		"--env", "NO_PROXY=127.0.0.1,localhost",
		"--env", "HIVE_CONTROL_TARGET_PORT=" + strconv.Itoa(cfg.HostPort),
		"--bind", cfg.ProxySocketPath + ":" + ProxySocketContainerPath,
		"--bind", cfg.ControlSocketDir + ":" + ControlSocketContainerDir,
		"--bind", cfg.Pack.ConfigDir + ":/opt/hive/pack:ro",
		"--bind", cfg.Pack.WorkingDir + ":/workspace",
	}

	// Optional: when set, actually enforces
	// apps/agent-engine/internal/controlclient's X-Session-API-Key header
	// (see LaunchConfig.SessionAPIKey's doc comment).
	if cfg.SessionAPIKey != "" {
		argv = append(argv, "--env", "SESSION_API_KEY="+cfg.SessionAPIKey)
	}

	// Issue #309 (blueprint Step 2.3) — bind-mount the tenant's enabled MCP
	// server config, when the caller resolved one, read-only next to the
	// pack config. Optional: an empty MCPConfigPath means this tenant has no
	// marketplace MCP servers enabled, or the caller could not reach
	// control-plane (marketplaceclient fails open — see cmd/agent-engine).
	if cfg.MCPConfigPath != "" {
		argv = append(argv, "--bind", cfg.MCPConfigPath+":"+MCPConfigContainerPath+":ro")
	}

	argv = append(argv,
		cfg.SIFPath,
		"--host", "0.0.0.0",
		"--port", strconv.Itoa(cfg.HostPort),
	)

	if containsDockerSocketReference(argv) {
		return nil, ErrDockerSocketReferenced
	}
	// Kept as a live assertion (not just a test): if a future edit ever
	// drops a required flag from the literal argv above, BuildArgv fails
	// closed instead of the gap only being caught by a test running later.
	if err := ValidateSecurityDefaults(argv); err != nil {
		return nil, err
	}
	return argv, nil
}

// CheckDockerSocketUnreachable reports an error if dockerHostEnv is
// non-empty or a file exists at socketPath — the two Docker-reachability
// signals security spike #307 rows 8/9 tested for ("no docker.sock ... none
// reachable from inside a tenant Apptainer sandbox").
func CheckDockerSocketUnreachable(dockerHostEnv, socketPath string) error {
	if dockerHostEnv != "" {
		return fmt.Errorf("%w: DOCKER_HOST=%q is set", ErrDockerSocketReferenced, dockerHostEnv)
	}
	if _, err := os.Stat(socketPath); err == nil {
		return fmt.Errorf("%w: %s exists", ErrDockerSocketReferenced, socketPath)
	}
	return nil
}

// AssertNoDockerSocketReachable checks the current process's real
// environment. It is the standing regression guard for security spike #307
// rows 8/9: CI (or a launched sandbox) running this proves the Docker
// socket stays unreachable from the agent-engine process.
func AssertNoDockerSocketReachable() error {
	return CheckDockerSocketUnreachable(os.Getenv("DOCKER_HOST"), dockerSocketPath)
}

// WriteEmptyFileForTest creates an empty file at path. Test-only helper so
// launcher_test.go can simulate a Docker-socket-like file existing without
// depending on an internal package for os.WriteFile.
func WriteEmptyFileForTest(path string) error {
	return os.WriteFile(path, nil, 0o600)
}
