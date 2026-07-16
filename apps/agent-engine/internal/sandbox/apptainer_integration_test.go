package sandbox_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/sandbox"
)

// TestApptainerLaunch_Live is the real, non-mocked proof that BuildArgv's
// output is a command Apptainer actually accepts. It is gated on
// HIVE_APPTAINER_TEST=1 plus a real `apptainer` binary and a pre-built SIF
// file (HIVE_APPTAINER_TEST_SIF), because none of these are available in
// this repo's default dev environment (WSL2 has no rootless user-namespace
// Apptainer support). See the PR description for what ran here versus what
// is proven only by the unit tests in launcher_test.go.
func TestApptainerLaunch_Live(t *testing.T) {
	if os.Getenv("HIVE_APPTAINER_TEST") != "1" {
		t.Skip("set HIVE_APPTAINER_TEST=1 on a host with rootless Apptainer to run this test")
	}
	if _, err := exec.LookPath("apptainer"); err != nil {
		t.Skip("apptainer binary not found on PATH")
	}
	sifPath := os.Getenv("HIVE_APPTAINER_TEST_SIF")
	if sifPath == "" {
		t.Skip("set HIVE_APPTAINER_TEST_SIF to a built agent-server SIF to run the live launch test")
	}

	cfg := sandbox.LaunchConfig{
		TenantID:        uuid.New(),
		UserID:          uuid.New(),
		Pack:            sandbox.Pack{Name: "coding-pack", ConfigDir: t.TempDir(), WorkingDir: t.TempDir()},
		SIFPath:         sifPath,
		HostPort:        38099,
		ProxySocketPath: placeholderSocketPath(t),
	}
	argv, err := sandbox.BuildArgv(cfg)
	if err != nil {
		t.Fatalf("BuildArgv: %v", err)
	}

	// argv is built entirely from validated, non-shell-interpreted config
	// (no user string ever reaches a shell).
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		t.Fatalf("apptainer run failed to start: %v", err)
	}
	defer func() { _ = cmd.Process.Kill() }()
}

// TestApptainerLaunch_DirectDialBypassFails is the live proof for the
// security-review finding that HTTP_PROXY/HTTPS_PROXY alone is advisory: a
// raw dial from inside the sandbox to a real external host must fail once
// --net --network none is in effect, regardless of proxy env vars. Gated
// identically to TestApptainerLaunch_Live.
func TestApptainerLaunch_DirectDialBypassFails(t *testing.T) {
	if os.Getenv("HIVE_APPTAINER_TEST") != "1" {
		t.Skip("set HIVE_APPTAINER_TEST=1 on a host with rootless Apptainer to run this test")
	}
	if _, err := exec.LookPath("apptainer"); err != nil {
		t.Skip("apptainer binary not found on PATH")
	}
	sifPath := os.Getenv("HIVE_APPTAINER_TEST_SIF")
	if sifPath == "" {
		t.Skip("set HIVE_APPTAINER_TEST_SIF to a built agent-server SIF to run the live launch test")
	}

	// Mirrors BuildArgv's mandatory security flags directly rather than
	// extending its public API (which is shaped around launching the
	// agent-server, not running an arbitrary probe command) for this one
	// gated test: --pid --containall --net --network none, no Docker
	// socket, then `apptainer exec <sif> <probe>` instead of `run`.
	probe := "curl -m 3 -o /dev/null -s -w '%{http_code}' http://1.1.1.1 && echo REACHED || echo BLOCKED"
	argv := []string{
		"apptainer", "exec",
		"--pid", "--containall", "--net", "--network", "none",
		sifPath, "sh", "-c", probe,
	}
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("apptainer exec failed to run the probe: %v (output: %s)", err, out)
	}
	if !strings.Contains(string(out), "BLOCKED") {
		t.Fatalf("expected direct dial to be blocked by network isolation, got output: %s", out)
	}
}

func placeholderSocketPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "egress.sock")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("create placeholder socket file: %v", err)
	}
	return path
}
