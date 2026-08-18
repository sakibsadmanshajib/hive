package main

// Daemon mode (issue #780). `agent-engine -serve <socket>` runs the same
// engine.SandboxEngine control-plane used to construct in-process, but as a
// long-lived host process behind a Unix socket.
//
// Why it exists: SandboxEngine execs the host's `apptainer` binary. On the
// demo and Enterprise single-box topology control-plane runs inside an
// Alpine container (deploy/docker/Dockerfile.control-plane) which has no
// glibc loader for that binary, no /dev/fuse, and no CAP_SYS_ADMIN-class
// privilege. Granting that container those privileges was considered and
// refused: it is the same process that holds the Stripe keys, the Supabase
// service-role key and the platform database DSN, and sandbox-escape-class
// privilege there is the worst place on the box to put it. Running the
// launcher as a separate unprivileged host process instead keeps Apptainer's
// rootless user-namespace launch exactly where it already works and leaves
// control-plane's capability set untouched.
//
// Trust boundary: the socket file itself. It is created 0600 under a
// directory only the deploying user and root can reach, and bind-mounted
// into control-plane; anyone who can open it can already read the host
// filesystem as that user. The shared internal token is checked as well so
// that a bind mount into one more container does not silently widen the
// boundary.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/engineapi"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/artifactsclient"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/egressclient"
)

// InternalTokenHeader is the shared-secret header control-plane sends. It
// mirrors the header control-plane's own internal endpoints already expect
// from this binary's egressclient calls.
const InternalTokenHeader = "X-Internal-Token"

type launchRequest struct {
	ID           uuid.UUID `json:"id"`
	TenantID     uuid.UUID `json:"tenant_id"`
	UserID       uuid.UUID `json:"user_id"`
	Pack         string    `json:"pack"`
	Instructions string    `json:"instructions"`
	// BearerJWT is the task's own user's bearer JWT, forwarded from
	// edge-api's task-create handler through control-plane untouched (see
	// apps/agent-engine/internal/engine.Task.BearerJWT's doc comment). Never
	// logged.
	BearerJWT string `json:"bearer_jwt"`
}

type launchResponse struct {
	SessionRef string `json:"session_ref"`
}

type sessionRequest struct {
	SessionRef string `json:"session_ref"`
}

type statusResponse struct {
	Status        string `json:"status"`
	ResultSummary string `json:"result_summary"`
	ErrorMessage  string `json:"error_message"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// serve runs the daemon until the process is signalled.
func serve(socketPath, controlPlaneURL, controlPlaneToken string) error {
	sifPath := os.Getenv("HIVE_AGENT_ENGINE_SIF_PATH")
	packsDir := os.Getenv("HIVE_AGENT_ENGINE_PACKS_DIR")
	workspaceRoot := os.Getenv("HIVE_AGENT_ENGINE_WORKSPACE_ROOT")
	runDir := os.Getenv("HIVE_AGENT_ENGINE_RUN_DIR")
	// An agent profile ID is deliberately not accepted here. The sandbox runs
	// with --containall, so the agent-server resolves a profile against a
	// profile store on a filesystem that is a fresh empty container every
	// session and can never hold one. Inline agent settings, which need a
	// model alias, are the only shape that works, so require it outright
	// rather than let a profile-only launch report healthy and fail on every
	// task.
	llmModel := os.Getenv("HIVE_AGENT_ENGINE_LLM_MODEL")
	var missing []string
	for name, v := range map[string]string{
		"HIVE_AGENT_ENGINE_SIF_PATH":       sifPath,
		"HIVE_AGENT_ENGINE_PACKS_DIR":      packsDir,
		"HIVE_AGENT_ENGINE_WORKSPACE_ROOT": workspaceRoot,
		"HIVE_AGENT_ENGINE_RUN_DIR":        runDir,
		"HIVE_AGENT_ENGINE_LLM_MODEL":      llmModel,
	} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("agent-engine: -serve needs %v", missing)
	}
	for _, dir := range []string{workspaceRoot, runDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("agent-engine: create %s: %w", dir, err)
		}
	}

	llmBaseURL := os.Getenv("HIVE_AGENT_ENGINE_LLM_BASE_URL")
	llmHost, err := hostOf(llmBaseURL)
	if err != nil {
		return fmt.Errorf("agent-engine: HIVE_AGENT_ENGINE_LLM_BASE_URL: %w", err)
	}

	egress := egressclient.New(controlPlaneURL, controlPlaneToken)
	// The tenant's effective egress policy governs what the agent's own
	// shell may reach. The model endpoint is not that: it is Hive's own
	// metered gateway, the sandbox has no other way to reach a model, and
	// egress.Effective returns an empty (deny-all) allowlist for any tenant
	// with no policy row, which would make every task fail on its first LLM
	// call. So the model host is appended to whatever the policy resolves
	// to, and nothing else is.
	resolveEgressHosts := func(ctx context.Context, tenantID, userID uuid.UUID) ([]string, error) {
		hosts, err := egress.Effective(ctx, tenantID, userID)
		if err != nil {
			return nil, err
		}
		if llmHost == "" {
			return hosts, nil
		}
		// Copied rather than appended in place: append can write into the
		// backing array egress.Effective returned.
		return append(append([]string(nil), hosts...), llmHost), nil
	}

	engineCfg := engineapi.Config{
		SIFPath:       sifPath,
		PacksDir:      packsDir,
		WorkspaceRoot: workspaceRoot,
		RunDir:        runDir,
		// Fails the launch when the policy cannot be resolved, exactly as
		// the in-process wiring does: never launch with an unknown egress
		// policy.
		ResolveEgressHosts:     resolveEgressHosts,
		LLMModel:               llmModel,
		LLMBaseURL:             llmBaseURL,
		LLMAPIKey:              os.Getenv("HIVE_AGENT_ENGINE_LLM_API_KEY"),
		SessionAPIKey:          os.Getenv("HIVE_AGENT_ENGINE_SESSION_API_KEY"),
		QuotaTenantConcurrency: envInt("HIVE_QUOTA_TENANT_CONCURRENCY", 4),
		QuotaUserConcurrency:   envInt("HIVE_QUOTA_USER_CONCURRENCY", 2),
		MemoryLimit:            envOr("HIVE_SANDBOX_MEMORY_LIMIT", "4G"),
		CPULimit:               envOr("HIVE_SANDBOX_CPU_LIMIT", "2"),
		PidsLimit:              envInt("HIVE_SANDBOX_PIDS_LIMIT", 512),
	}
	// EDGE_API_URL is optional: an operator who sets it to the empty string
	// leaves engineCfg.Publisher at its true zero value (a nil interface, not
	// an interface wrapping a nil *artifactsclient.Client — those are not
	// the same thing in Go, and the latter would panic the first time
	// engine.SandboxEngine.Status tried to call through it), so
	// knowledge-work-pack deck output just never publishes and Status keeps
	// returning the agent's own final-response text exactly as it always
	// has. "http://edge-api:8080" only resolves for this daemon's own host
	// when it runs inside the compose network; the real host launcher
	// (scripts/install-agent-engine-host.sh) always sets this explicitly to
	// the box's published edge-api port instead, mirroring how that script
	// already overrides CONTROL_PLANE_URL's own compose-shaped default.
	if edgeAPIURL := envOr("EDGE_API_URL", "http://edge-api:8080"); edgeAPIURL != "" {
		engineCfg.Publisher = artifactsclient.New(edgeAPIURL)
	}
	engine := engineapi.New(engineCfg)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /launch", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r, controlPlaneToken) {
			return
		}
		var req launchRequest
		if !decode(w, r, &req) {
			return
		}
		ref, err := engine.Launch(r.Context(), engineapi.Task{
			ID:           req.ID,
			TenantID:     req.TenantID,
			UserID:       req.UserID,
			Pack:         req.Pack,
			Instructions: req.Instructions,
			BearerJWT:    req.BearerJWT,
		})
		if err != nil {
			log.Printf("agent-engine: launch task %s: %v", req.ID, err)
			writeJSON(w, http.StatusBadGateway, errorResponse{Error: err.Error()})
			return
		}
		log.Printf("agent-engine: launched task %s as session %s", req.ID, ref)
		writeJSON(w, http.StatusOK, launchResponse{SessionRef: ref})
	})
	mux.HandleFunc("POST /status", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r, controlPlaneToken) {
			return
		}
		var req sessionRequest
		if !decode(w, r, &req) {
			return
		}
		status, summary, errMessage, err := engine.Status(r.Context(), req.SessionRef)
		if err != nil {
			code := http.StatusBadGateway
			if errors.Is(err, engineapi.ErrUnknownSession) {
				code = http.StatusNotFound
			}
			writeJSON(w, code, errorResponse{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, statusResponse{
			Status:        string(status),
			ResultSummary: summary,
			ErrorMessage:  errMessage,
		})
	})
	mux.HandleFunc("POST /cancel", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(w, r, controlPlaneToken) {
			return
		}
		var req sessionRequest
		if !decode(w, r, &req) {
			return
		}
		if err := engine.Cancel(r.Context(), req.SessionRef); err != nil {
			code := http.StatusBadGateway
			if errors.Is(err, engineapi.ErrUnknownSession) {
				code = http.StatusNotFound
			}
			writeJSON(w, code, errorResponse{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, struct{}{})
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// A leftover socket from a killed process would make Listen fail with
	// EADDRINUSE even though nothing is serving it. The deploy restarts this
	// daemon on every deploy, so that is the normal path, not the rare one.
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("agent-engine: remove stale socket %s: %w", socketPath, err)
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("agent-engine: listen on %s: %w", socketPath, err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		return fmt.Errorf("agent-engine: chmod %s: %w", socketPath, err)
	}

	// Every request body here is one small JSON object capped at 1 MiB by
	// decode, so a read that takes longer than this is a stuck peer rather
	// than a slow one. WriteTimeout is deliberately left unset: /launch
	// legitimately holds the response open while the sandbox starts and the
	// conversation is submitted, which takes minutes on a cold container.
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	log.Printf("agent-engine: serving on %s (sif=%s, packs=%s)", socketPath, sifPath, packsDir)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// hostOf returns the hostname of an absolute http(s) URL, or "" for an empty
// input. Anything else is an error: a mistyped base URL must fail at start-up
// rather than silently leaving the model endpoint off the allowlist and
// failing every task later with an opaque proxy denial.
func hostOf(rawURL string) (string, error) {
	if rawURL == "" {
		return "", nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("expected an http(s) URL, got %q", rawURL)
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("no host in %q", rawURL)
	}
	return u.Hostname(), nil
}

// authorized enforces the shared internal token when one is configured.
func authorized(w http.ResponseWriter, r *http.Request, token string) bool {
	if token == "" || r.Header.Get(InternalTokenHeader) == token {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
	return false
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
