package sandbox_test

import (
	"errors"
	"fmt"
	"os"
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
		SIFPath:          "/srv/hive/sif/agent-server.sif",
		HostPort:         38080,
		ProxySocketPath:  "/srv/hive/run/t1/egress.sock",
		ControlSocketDir: "/srv/hive/run/t1/control",
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

func TestBuildArgv_AlwaysIsolatesNetwork(t *testing.T) {
	// The core fix for the "raw socket bypasses HTTP_PROXY" security review
	// finding: --net --network none is the only rootless-permitted network
	// config (verified against Apptainer docs 2026-07-16), and it leaves the
	// sandbox with no interface to dial out on except loopback.
	argv, err := sandbox.BuildArgv(validConfig())
	if err != nil {
		t.Fatalf("BuildArgv: %v", err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--net --network none") {
		t.Fatalf("expected --net --network none present as an adjacent pair, got argv: %v", argv)
	}
}

func TestBuildArgv_WiresProxyEnv(t *testing.T) {
	cfg := validConfig()
	argv, err := sandbox.BuildArgv(cfg)
	if err != nil {
		t.Fatalf("BuildArgv: %v", err)
	}
	joined := strings.Join(argv, " ")
	wantProxy := fmt.Sprintf("http://127.0.0.1:%d", sandbox.ProxyShimPort)
	if !strings.Contains(joined, "HTTP_PROXY="+wantProxy) {
		t.Fatalf("expected HTTP_PROXY wired to %s, got argv: %v", wantProxy, argv)
	}
	if !strings.Contains(joined, "HTTPS_PROXY="+wantProxy) {
		t.Fatalf("expected HTTPS_PROXY wired to %s, got argv: %v", wantProxy, argv)
	}
	wantBind := cfg.ProxySocketPath + ":" + sandbox.ProxySocketContainerPath
	if !strings.Contains(joined, wantBind) {
		t.Fatalf("expected proxy socket bind mount %s, got argv: %v", wantBind, argv)
	}
}

func TestBuildArgv_WiresControlSocket(t *testing.T) {
	cfg := validConfig()
	argv, err := sandbox.BuildArgv(cfg)
	if err != nil {
		t.Fatalf("BuildArgv: %v", err)
	}
	joined := strings.Join(argv, " ")
	wantBind := cfg.ControlSocketDir + ":" + sandbox.ControlSocketContainerDir
	if !strings.Contains(joined, wantBind) {
		t.Fatalf("expected control socket bind mount %s, got argv: %v", wantBind, argv)
	}
	wantEnv := fmt.Sprintf("HIVE_CONTROL_TARGET_PORT=%d", cfg.HostPort)
	if !strings.Contains(joined, wantEnv) {
		t.Fatalf("expected control target port env %s, got argv: %v", wantEnv, argv)
	}
	if err := sandbox.ValidateSecurityDefaults(argv); err != nil {
		t.Fatalf("expected control socket bind to satisfy ValidateSecurityDefaults: %v", err)
	}
}

func TestBuildArgv_OmitsSessionAPIKeyEnvWhenUnset(t *testing.T) {
	argv, err := sandbox.BuildArgv(validConfig())
	if err != nil {
		t.Fatalf("BuildArgv: %v", err)
	}
	if strings.Contains(strings.Join(argv, " "), "SESSION_API_KEY=") {
		t.Fatalf("expected no SESSION_API_KEY env when unset, got argv: %v", argv)
	}
}

func TestBuildArgv_WiresSessionAPIKeyEnvWhenSet(t *testing.T) {
	cfg := validConfig()
	cfg.SessionAPIKey = "s3cr3t"
	argv, err := sandbox.BuildArgv(cfg)
	if err != nil {
		t.Fatalf("BuildArgv: %v", err)
	}
	if !strings.Contains(strings.Join(argv, " "), "SESSION_API_KEY=s3cr3t") {
		t.Fatalf("expected SESSION_API_KEY=s3cr3t in argv, got: %v", argv)
	}
}

func TestControlSocketPath(t *testing.T) {
	cfg := validConfig()
	got := sandbox.ControlSocketPath(cfg)
	want := cfg.ControlSocketDir + "/" + sandbox.ControlSocketFileName
	if got != want {
		t.Fatalf("ControlSocketPath = %q, want %q", got, want)
	}
}

func TestBuildArgv_OmitsMCPConfigBindWhenPathEmpty(t *testing.T) {
	argv, err := sandbox.BuildArgv(validConfig())
	if err != nil {
		t.Fatalf("BuildArgv: %v", err)
	}
	if strings.Contains(strings.Join(argv, " "), sandbox.MCPConfigContainerPath) {
		t.Fatalf("expected no MCP config bind mount when MCPConfigPath is empty, got argv: %v", argv)
	}
}

func TestBuildArgv_BindsMCPConfigWhenPathSet(t *testing.T) {
	cfg := validConfig()
	cfg.MCPConfigPath = "/srv/hive/run/t1/mcp_config.json"
	argv, err := sandbox.BuildArgv(cfg)
	if err != nil {
		t.Fatalf("BuildArgv: %v", err)
	}
	wantBind := cfg.MCPConfigPath + ":" + sandbox.MCPConfigContainerPath + ":ro"
	if !strings.Contains(strings.Join(argv, " "), wantBind) {
		t.Fatalf("expected MCP config bind mount %s, got argv: %v", wantBind, argv)
	}
}

func TestBuildArgv_RejectsDockerSocketInMCPConfigPath(t *testing.T) {
	cfg := validConfig()
	cfg.MCPConfigPath = "/var/run/docker.sock"
	if _, err := sandbox.BuildArgv(cfg); !errors.Is(err, sandbox.ErrDockerSocketReferenced) {
		t.Fatalf("expected ErrDockerSocketReferenced, got %v", err)
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
		{"empty proxy socket path", func(c *sandbox.LaunchConfig) { c.ProxySocketPath = "" }, sandbox.ErrMissingProxySocketPath},
		{"empty control socket dir", func(c *sandbox.LaunchConfig) { c.ControlSocketDir = "" }, sandbox.ErrMissingControlSocketDir},
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

func TestBuildArgv_RejectsDockerSocketInProxySocketPath(t *testing.T) {
	cfg := validConfig()
	cfg.ProxySocketPath = "/var/run/docker.sock"
	if _, err := sandbox.BuildArgv(cfg); !errors.Is(err, sandbox.ErrDockerSocketReferenced) {
		t.Fatalf("expected ErrDockerSocketReferenced, got %v", err)
	}
}

func TestBuildArgv_RejectsDockerSocketInControlSocketDir(t *testing.T) {
	cfg := validConfig()
	cfg.ControlSocketDir = "/var/run/docker.sock"
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
	argv := []string{
		"apptainer", "run", "--pid", "--containall", "--net", "--network", "none",
		"--bind", "/srv/hive/run/t1/control:" + sandbox.ControlSocketContainerDir,
	}
	if err := sandbox.ValidateSecurityDefaults(argv); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestValidateSecurityDefaults_CatchesMissingNetworkIsolation(t *testing.T) {
	// Proves the validator catches an argv that has --pid/--containall but
	// no network isolation at all — the gap the security review flagged:
	// HTTP_PROXY/HTTPS_PROXY alone is advisory and a raw socket bypasses it.
	argv := []string{"apptainer", "run", "--pid", "--containall"}
	err := sandbox.ValidateSecurityDefaults(argv)
	if err == nil {
		t.Fatal("expected error for argv missing --net --network none")
	}
	if !strings.Contains(err.Error(), "--network none") {
		t.Fatalf("expected error to name the missing network isolation flag, got: %v", err)
	}
}

func TestValidateSecurityDefaults_RejectsNonNoneNetwork(t *testing.T) {
	// "none" is the only network config Apptainer permits rootless users to
	// request; any other value (bridge, ptp, ...) either requires setuid
	// configuration or reintroduces a routable interface, so it must not
	// satisfy the validator even though "--network" is present.
	argv := []string{"apptainer", "run", "--pid", "--containall", "--net", "--network", "bridge"}
	if err := sandbox.ValidateSecurityDefaults(argv); err == nil {
		t.Fatal("expected error for --network bridge (only \"none\" is permitted)")
	}
}

func TestValidateSecurityDefaults_CatchesMissingControlSocketBind(t *testing.T) {
	// Proves the validator would catch a future edit accidentally dropping
	// the control-channel bind mount (issue #305 Wave 3 gap) the same way it
	// already catches a dropped --pid/--containall/--network isolation flag.
	argv := []string{"apptainer", "run", "--pid", "--containall", "--net", "--network", "none"}
	err := sandbox.ValidateSecurityDefaults(argv)
	if err == nil {
		t.Fatal("expected error for argv missing the control socket bind mount")
	}
	if !strings.Contains(err.Error(), sandbox.ControlSocketContainerDir) {
		t.Fatalf("expected error to name the missing control socket bind target, got: %v", err)
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
	// This asserts a fact about the real host's filesystem
	// (/var/run/docker.sock), not about agent-engine's own isolation: it is
	// only meaningful on a target host that mirrors the actual deployed
	// environment (no Docker installed alongside agent-engine), the same
	// class of environment TestApptainerLaunch_Live requires. A stock
	// GitHub Actions runner (and most dev laptops) has Docker installed for
	// unrelated reasons and legitimately has this socket present, which is
	// not a regression; gated identically to the live Apptainer tests in
	// apptainer_integration_test.go rather than unconditionally in CI.
	if os.Getenv("HIVE_APPTAINER_TEST") != "1" {
		t.Skip("set HIVE_APPTAINER_TEST=1 on a target host with no Docker installed alongside agent-engine to run this test")
	}
	if err := sandbox.AssertNoDockerSocketReachable(); err != nil {
		t.Fatalf("docker socket must not be reachable from agent-engine: %v", err)
	}
}

func writeFile(path string) error {
	return sandbox.WriteEmptyFileForTest(path)
}
