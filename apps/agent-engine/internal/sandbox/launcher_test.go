package sandbox_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/sandbox"
)

func validConfig() sandbox.LaunchConfig {
	return sandbox.LaunchConfig{
		TenantID: uuid.New(),
		UserID:   uuid.New(),
		Pack: sandbox.Pack{
			Name:       "coding-pack",
			ConfigDir:  "/srv/hive/packs/coding-pack",
			WorkingDir: "/srv/hive/workspaces/t1",
		},
		SIFPath:   "/srv/hive/sif/agent-server.sif",
		HostPort:  38080,
		ProxyAddr: "127.0.0.1:38081",
	}
}

func TestBuildArgv_AlwaysIncludesPidAndContainall(t *testing.T) {
	argv, err := sandbox.BuildArgv(validConfig())
	if err != nil {
		t.Fatalf("BuildArgv: %v", err)
	}
	if err := sandbox.ValidateSecurityDefaults(argv); err != nil {
		t.Fatalf("expected mandatory security flags present: %v", err)
	}
}

func TestBuildArgv_WiresProxyEnv(t *testing.T) {
	cfg := validConfig()
	argv, err := sandbox.BuildArgv(cfg)
	if err != nil {
		t.Fatalf("BuildArgv: %v", err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "HTTP_PROXY=http://"+cfg.ProxyAddr) {
		t.Fatalf("expected HTTP_PROXY wired to %s, got argv: %v", cfg.ProxyAddr, argv)
	}
	if !strings.Contains(joined, "HTTPS_PROXY=http://"+cfg.ProxyAddr) {
		t.Fatalf("expected HTTPS_PROXY wired to %s, got argv: %v", cfg.ProxyAddr, argv)
	}
}

func TestBuildArgv_RejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(c *sandbox.LaunchConfig)
		wantErr error
	}{
		{"nil tenant", func(c *sandbox.LaunchConfig) { c.TenantID = uuid.Nil }, sandbox.ErrNilTenant},
		{"nil user", func(c *sandbox.LaunchConfig) { c.UserID = uuid.Nil }, sandbox.ErrNilUser},
		{"empty SIF path", func(c *sandbox.LaunchConfig) { c.SIFPath = "" }, sandbox.ErrMissingSIFPath},
		{"empty proxy addr", func(c *sandbox.LaunchConfig) { c.ProxyAddr = "" }, sandbox.ErrMissingProxyAddr},
		{"empty pack config dir", func(c *sandbox.LaunchConfig) { c.Pack.ConfigDir = "" }, sandbox.ErrMissingConfigDir},
		{"empty pack working dir", func(c *sandbox.LaunchConfig) { c.Pack.WorkingDir = "" }, sandbox.ErrMissingWorkingDir},
		{"zero host port", func(c *sandbox.LaunchConfig) { c.HostPort = 0 }, sandbox.ErrInvalidHostPort},
		{"negative host port", func(c *sandbox.LaunchConfig) { c.HostPort = -1 }, sandbox.ErrInvalidHostPort},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(&cfg)
			if _, err := sandbox.BuildArgv(cfg); !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestBuildArgv_RejectsDockerSocketInWorkingDir(t *testing.T) {
	cfg := validConfig()
	cfg.Pack.WorkingDir = "/var/run/docker.sock"
	if _, err := sandbox.BuildArgv(cfg); !errors.Is(err, sandbox.ErrDockerSocketReferenced) {
		t.Fatalf("expected ErrDockerSocketReferenced, got %v", err)
	}
}

func TestBuildArgv_RejectsDockerSocketInConfigDir(t *testing.T) {
	cfg := validConfig()
	cfg.Pack.ConfigDir = "/some/path/docker.sock"
	if _, err := sandbox.BuildArgv(cfg); !errors.Is(err, sandbox.ErrDockerSocketReferenced) {
		t.Fatalf("expected ErrDockerSocketReferenced, got %v", err)
	}
}

func TestValidateSecurityDefaults_CatchesMissingFlags(t *testing.T) {
	// Proves the validator would have caught the exact gap security spike
	// #307 found in upstream's ApptainerWorkspace._start_container(), which
	// runs `apptainer run --fakeroot --compat` with neither --pid nor
	// --containall (default shares the host PID namespace).
	upstreamDefaultArgv := []string{"apptainer", "run", "--fakeroot", "--compat"}
	err := sandbox.ValidateSecurityDefaults(upstreamDefaultArgv)
	if err == nil {
		t.Fatal("expected error for argv missing --pid and --containall")
	}
	if !strings.Contains(err.Error(), "--pid") || !strings.Contains(err.Error(), "--containall") {
		t.Fatalf("expected error to name both missing flags, got: %v", err)
	}
}

func TestValidateSecurityDefaults_PassesCompleteArgv(t *testing.T) {
	argv := []string{"apptainer", "run", "--pid", "--containall"}
	if err := sandbox.ValidateSecurityDefaults(argv); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestCheckDockerSocketUnreachable(t *testing.T) {
	dir := t.TempDir()
	missing := dir + "/docker.sock"

	if err := sandbox.CheckDockerSocketUnreachable("", missing); err != nil {
		t.Fatalf("expected nil for missing socket and unset DOCKER_HOST, got %v", err)
	}
	if err := sandbox.CheckDockerSocketUnreachable("tcp://127.0.0.1:2375", missing); err == nil {
		t.Fatal("expected error when DOCKER_HOST is set")
	}

	present := dir + "/present.sock"
	if err := writeFile(present); err != nil {
		t.Fatalf("write fake socket file: %v", err)
	}
	if err := sandbox.CheckDockerSocketUnreachable("", present); err == nil {
		t.Fatal("expected error when socket path exists")
	}
}

func TestAssertNoDockerSocketReachable_PassesInThisEnvironment(t *testing.T) {
	// The toolchain container this test runs in never mounts or runs Docker
	// inside the agent-engine process itself (security spike #307 rows
	// 8/9); this is the standing regression guard for that fact.
	if err := sandbox.AssertNoDockerSocketReachable(); err != nil {
		t.Fatalf("docker socket must not be reachable from agent-engine: %v", err)
	}
}

func writeFile(path string) error {
	return sandbox.WriteEmptyFileForTest(path)
}
