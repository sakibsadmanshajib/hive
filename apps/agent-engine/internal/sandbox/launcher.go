// Package sandbox constructs the Apptainer rootless launch command for a
// coding-pack or knowledge-work-pack agent-engine session (issue #305/#308).
// Every invocation this package builds carries two enforced defaults proven
// necessary by security spike #307:
//
//   - --pid and --containall: Apptainer's default shares the host PID
//     namespace (spike implementation condition 1) — the upstream
//     ApptainerWorkspace this package wraps (vendor/openhands) does not
//     pass either by default.
//   - egress is bounded by routing all sandbox HTTP/HTTPS traffic through a
//     per-session egressproxy.Proxy scoped to the tenant/user's effective
//     allowed_hosts (apps/control-plane/internal/egress, issue #308), never
//     the host's unrestricted network.
//
// BuildArgv also refuses to launch if any bind mount or working directory in
// the constructed command references the Docker socket (spike rows 8/9,
// issue #307).
package sandbox

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

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
	TenantID  uuid.UUID
	UserID    uuid.UUID
	Pack      Pack
	SIFPath   string
	HostPort  int
	ProxyAddr string // host:port of the per-session egressproxy.Proxy
}

var (
	ErrMissingSIFPath    = errors.New("sandbox: SIFPath is required")
	ErrMissingProxyAddr  = errors.New("sandbox: ProxyAddr is required")
	ErrMissingConfigDir  = errors.New("sandbox: Pack.ConfigDir is required")
	ErrMissingWorkingDir = errors.New("sandbox: Pack.WorkingDir is required")
	ErrInvalidHostPort   = errors.New("sandbox: HostPort must be positive")
	ErrNilTenant         = errors.New("sandbox: TenantID must not be uuid.Nil")
	ErrNilUser           = errors.New("sandbox: UserID must not be uuid.Nil")

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
	case c.ProxyAddr == "":
		return ErrMissingProxyAddr
	case c.Pack.ConfigDir == "":
		return ErrMissingConfigDir
	case c.Pack.WorkingDir == "":
		return ErrMissingWorkingDir
	case c.HostPort <= 0:
		return ErrInvalidHostPort
	}
	return nil
}

// RequiredSecurityFlags are the flags security spike #307 proved must always
// be present on every Apptainer invocation this package (or any future
// patched vendor copy of the upstream ApptainerWorkspace) constructs.
var RequiredSecurityFlags = []string{"--pid", "--containall"}

// ValidateSecurityDefaults reports an error if argv is missing any flag in
// RequiredSecurityFlags.
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
	if len(missing) > 0 {
		return fmt.Errorf("sandbox: missing required security flags: %s", strings.Join(missing, ", "))
	}
	return nil
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
// unconditionally includes --pid and --containall and wires
// HTTP_PROXY/HTTPS_PROXY at cfg.ProxyAddr so all sandbox egress is bound by
// the caller's egressproxy.Proxy allowlist. BuildArgv refuses
// (ErrDockerSocketReferenced) if the resulting command would reference the
// Docker socket in any form.
func BuildArgv(cfg LaunchConfig) (argv []string, err error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	proxyURL := "http://" + cfg.ProxyAddr

	argv = []string{
		"apptainer", "run",
		"--pid",
		"--containall",
		"--no-mount", "hostfs",
		"--no-mount", "bind-paths",
		"--env", "HTTP_PROXY=" + proxyURL,
		"--env", "HTTPS_PROXY=" + proxyURL,
		"--env", "NO_PROXY=127.0.0.1,localhost",
		"--bind", cfg.Pack.ConfigDir + ":/opt/hive/pack:ro",
		"--bind", cfg.Pack.WorkingDir + ":/workspace",
		cfg.SIFPath,
		"--host", "0.0.0.0",
		"--port", strconv.Itoa(cfg.HostPort),
	}

	if containsDockerSocketReference(argv) {
		return nil, ErrDockerSocketReferenced
	}
	// Kept as a live assertion (not just a test): if a future edit ever
	// drops --pid/--containall from the literal argv above, BuildArgv fails
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
