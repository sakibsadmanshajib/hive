// Command agent-engine launches one Apptainer rootless coding-pack or
// knowledge-work-pack sandbox session for a tenant/user, enforcing the
// per-tenant/user quota (internal/quota), the egress-policy allowlist
// (internal/egressclient, internal/egressproxy), and the mandatory
// --pid/--containall security defaults (internal/sandbox). It is the manual
// smoke-test entrypoint for this PR and the integration point Wave 3
// (edge-api/OWUI task lifecycle) will call into.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/egressclient"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/egressproxy"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/marketplaceclient"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/quota"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/sandbox"
)

// insecureDefaultToken is docker-compose.yml's local/dev fallback value for
// CONTROL_PLANE_INTERNAL_TOKEN. Starting with it (or with an empty token)
// outside local/dev would silently accept requests to the control-plane's
// internal endpoints under a token every clone of this repo knows.
const insecureDefaultToken = "hive-local-internal-token"

func main() {
	tenantID := flag.String("tenant", "", "tenant UUID")
	userID := flag.String("user", "", "user UUID")
	pack := flag.String("pack", "coding-pack", "pack name: coding-pack or knowledge-work-pack")
	sifPath := flag.String("sif", os.Getenv("HIVE_AGENT_SIF_PATH"), "path to the built agent-server SIF")
	workingDir := flag.String("workspace", "", "host directory bind-mounted as /workspace")
	hostPort := flag.Int("port", 38080, "host port the agent-server listens on")
	controlSocketDir := flag.String("control-socket-dir", "", "host directory bind-mounted for the host<->agent-server control channel (issue #305); auto-created under a temp dir when empty")
	controlPlaneURL := flag.String("control-plane-url", envOr("CONTROL_PLANE_URL", "http://control-plane:8081"), "control-plane base URL")
	controlPlaneToken := flag.String("control-plane-token", os.Getenv("CONTROL_PLANE_INTERNAL_TOKEN"), "shared internal-service token")
	dryRun := flag.Bool("dry-run", false, "print the constructed apptainer argv and exit without launching")
	flag.Parse()

	tenant, err := uuid.Parse(*tenantID)
	if err != nil {
		log.Fatalf("agent-engine: invalid -tenant: %v", err)
	}
	user, err := uuid.Parse(*userID)
	if err != nil {
		log.Fatalf("agent-engine: invalid -user: %v", err)
	}
	if *workingDir == "" {
		log.Fatal("agent-engine: -workspace is required")
	}
	if *sifPath == "" {
		log.Fatal("agent-engine: -sif (or HIVE_AGENT_SIF_PATH) is required")
	}
	if (*controlPlaneToken == "" || *controlPlaneToken == insecureDefaultToken) && os.Getenv("HIVE_AGENT_ENGINE_ALLOW_DEFAULT_TOKEN") != "1" {
		log.Fatal("agent-engine: CONTROL_PLANE_INTERNAL_TOKEN is empty or the known local/dev default; refusing to start. Set HIVE_AGENT_ENGINE_ALLOW_DEFAULT_TOKEN=1 only for local/dev.")
	}

	limits := quota.Limits{TenantConcurrency: envInt("HIVE_QUOTA_TENANT_CONCURRENCY", 4), UserConcurrency: envInt("HIVE_QUOTA_USER_CONCURRENCY", 2)}
	q, err := quota.New(limits)
	if err != nil {
		log.Fatalf("agent-engine: quota.New: %v", err)
	}

	release, err := q.Acquire(tenant, user)
	if err != nil {
		log.Fatalf("agent-engine: quota exceeded, refusing to launch: %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := egressclient.New(*controlPlaneURL, *controlPlaneToken)
	allowedHosts, err := client.Effective(ctx, tenant, user)
	if err != nil {
		// Fail closed: never launch with an unbounded egress policy just
		// because the control-plane could not be reached.
		log.Fatalf("agent-engine: could not resolve egress policy, refusing to launch: %v", err)
	}

	// The proxy listens on a Unix socket, not a TCP port: the sandbox launches
	// with --net --network none (internal/sandbox), so a TCP loopback
	// listener on the host would not be reachable from inside it anyway.
	// Bind mounts cross the network-namespace boundary because they are a
	// filesystem operation, not a network one.
	sockDir, err := os.MkdirTemp("", "hive-agent-engine-egress-*")
	if err != nil {
		log.Fatalf("agent-engine: could not create egress proxy socket dir: %v", err)
	}
	defer os.RemoveAll(sockDir)
	proxySocketPath := filepath.Join(sockDir, "egress.sock")

	proxyListener, err := net.Listen("unix", proxySocketPath)
	if err != nil {
		log.Fatalf("agent-engine: could not start egress proxy listener: %v", err)
	}
	proxySrv := &http.Server{Handler: egressproxy.New(allowedHosts)}
	go func() { _ = proxySrv.Serve(proxyListener) }()
	defer proxySrv.Close()

	// Issue #309 (blueprint Step 2.3) — resolve this tenant's enabled MCP
	// marketplace servers and bind-mount the resulting OpenHands-native
	// config next to the pack. Unlike the egress policy above, this fails
	// open: the marketplace is an additive capability, so a control-plane
	// outage or a tenant with nothing enabled must not block the sandbox
	// from launching at all, it just launches with no MCP servers
	// configured.
	mcpConfigPath := resolveMCPConfigPath(ctx, *controlPlaneURL, *controlPlaneToken, tenant, sockDir)

	// Host<->agent-server control channel (issue #305): a directory, empty
	// at launch time, bind-mounted read-write into the sandbox so the
	// in-SIF shim can create its listening socket inside it after start.
	// See apps/agent-engine/internal/sandbox's package doc and
	// apps/agent-engine/internal/controlclient.
	ctlDir := *controlSocketDir
	if ctlDir == "" {
		ctlDir, err = os.MkdirTemp("", "hive-agent-engine-control-*")
		if err != nil {
			log.Fatalf("agent-engine: could not create control socket dir: %v", err)
		}
		defer os.RemoveAll(ctlDir)
	}

	cfg := sandbox.LaunchConfig{
		TenantID: tenant,
		UserID:   user,
		Pack: sandbox.Pack{
			Name:       *pack,
			ConfigDir:  "packs/" + *pack,
			WorkingDir: *workingDir,
		},
		SIFPath:          *sifPath,
		HostPort:         *hostPort,
		ProxySocketPath:  proxySocketPath,
		ControlSocketDir: ctlDir,
		MCPConfigPath:    mcpConfigPath,
	}

	argv, err := sandbox.BuildArgv(cfg)
	if err != nil {
		log.Fatalf("agent-engine: BuildArgv: %v", err)
	}

	if *dryRun {
		fmt.Println(argv)
		return
	}

	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- argv is built entirely from validated, non-shell-interpreted config
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("agent-engine: apptainer run failed: %v", err)
	}
}

// resolveMCPConfigPath fetches tenant's enabled MCP marketplace servers
// (issue #309), writes the resulting OpenHands-native mcpServers JSON into
// dir, and returns the file path for sandbox.LaunchConfig.MCPConfigPath. It
// fails open: any error (unreachable control-plane, no servers enabled)
// logs a warning and returns "", which sandbox.BuildArgv treats as "mount
// nothing" rather than refusing to launch.
func resolveMCPConfigPath(ctx context.Context, controlPlaneURL, controlPlaneToken string, tenant uuid.UUID, dir string) string {
	client := marketplaceclient.New(controlPlaneURL, controlPlaneToken)
	entries, err := client.Enabled(ctx, tenant)
	if err != nil {
		log.Printf("agent-engine: could not resolve marketplace MCP servers, launching with none configured: %v", err)
		return ""
	}
	if len(entries) == 0 {
		return ""
	}

	config, err := marketplaceclient.BuildConfig(entries)
	if err != nil {
		log.Printf("agent-engine: could not build MCP config, launching with none configured: %v", err)
		return ""
	}

	path := filepath.Join(dir, "mcp_config.json")
	if err := os.WriteFile(path, config, 0o600); err != nil {
		log.Printf("agent-engine: could not write MCP config, launching with none configured: %v", err)
		return ""
	}
	return path
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return fallback
	}
	return n
}
