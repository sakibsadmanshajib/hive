package sandbox_test

import (
	"os"
	"os/exec"
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
		TenantID:  uuid.New(),
		UserID:    uuid.New(),
		Pack:      sandbox.Pack{Name: "coding-pack", ConfigDir: t.TempDir(), WorkingDir: t.TempDir()},
		SIFPath:   sifPath,
		HostPort:  38099,
		ProxyAddr: "127.0.0.1:38098",
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
