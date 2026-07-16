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
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/egressclient"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/egressproxy"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/quota"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/sandbox"
)

func main() {
	tenantID := flag.String("tenant", "", "tenant UUID")
	userID := flag.String("user", "", "user UUID")
	pack := flag.String("pack", "coding-pack", "pack name: coding-pack or knowledge-work-pack")
	sifPath := flag.String("sif", os.Getenv("HIVE_AGENT_SIF_PATH"), "path to the built agent-server SIF")
	workingDir := flag.String("workspace", "", "host directory bind-mounted as /workspace")
	hostPort := flag.Int("port", 38080, "host port the agent-server listens on")
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

	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("agent-engine: could not start egress proxy listener: %v", err)
	}
	proxyAddr := proxyListener.Addr().String()
	proxySrv := &http.Server{Handler: egressproxy.New(allowedHosts)}
	go func() { _ = proxySrv.Serve(proxyListener) }()
	defer proxySrv.Close()

	cfg := sandbox.LaunchConfig{
		TenantID: tenant,
		UserID:   user,
		Pack: sandbox.Pack{
			Name:       *pack,
			ConfigDir:  "packs/" + *pack,
			WorkingDir: *workingDir,
		},
		SIFPath:   *sifPath,
		HostPort:  *hostPort,
		ProxyAddr: proxyAddr,
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
