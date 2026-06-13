package litellmconfig

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"time"
)

const (
	defaultContainerName = "litellm"
	defaultSocketPath    = "/var/run/docker.sock"
	restartTimeout       = 30 * time.Second
)

// containerNameRe is the strict allowlist for Docker container names.
// Docker itself allows [a-zA-Z0-9][a-zA-Z0-9_.-]+ so we match that exactly.
// Validated at construction time to prevent path injection into the Docker
// Engine API URL (e.g. a value like "../images/prune" would target an
// unintended endpoint).
var containerNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// DockerRestarter signals a LiteLLM container restart via the Docker Engine
// HTTP API over a Unix socket. No `docker` CLI binary is required; only the
// socket mount is needed.
//
// Security note: mounting /var/run/docker.sock grants host-root-equivalent
// capability to this process. All calls are intentionally restricted to a
// single restart endpoint (POST /containers/<name>/restart). No exec, create,
// image, or other container operations are performed. The sync endpoint that
// triggers this restarter is guarded by RequireInternalToken so it is
// unreachable from the public internet.
//
// The restart API call uses:
//
//	POST /containers/<containerName>/restart?t=10
//
// over the Unix socket at socketPath (default /var/run/docker.sock).
// The socket must be mounted read-write (not :ro) in the container.
//
// TODO(litellm-db-mode): When LITELLM_CONFIG_MODE=db, call the LiteLLM admin
// API instead of restarting the container:
//   - POST /model/new        (with Authorization: Bearer <LITELLM_MASTER_KEY>)
//   - POST /model/update
//   - DELETE /model/delete
//
// Required env vars for DB mode:
//   - LITELLM_MASTER_KEY    — admin API key
//   - LITELLM_BASE_URL      — base URL of the LiteLLM proxy (e.g. http://litellm:4000)
//
// Confirm exact /model/* API shape via Context7 (LiteLLM docs) before implementing.
type DockerRestarter struct {
	containerName string
	socketPath    string
}

// NewDockerRestarter returns a DockerRestarter targeting the given container
// name and Unix socket path. Returns an error if containerName fails the
// Docker name allowlist (prevents path injection into the Docker Engine API).
func NewDockerRestarter(containerName, socketPath string) (*DockerRestarter, error) {
	if !containerNameRe.MatchString(containerName) {
		return nil, fmt.Errorf("litellmconfig: invalid container name %q: must match %s", containerName, containerNameRe)
	}
	return &DockerRestarter{
		containerName: containerName,
		socketPath:    socketPath,
	}, nil
}

// NewDefaultDockerRestarter returns a DockerRestarter whose container name is
// read from LITELLM_CONTAINER_NAME (default: "litellm"). The socketPath
// parameter allows tests to inject a fake socket; pass "" to use the
// production default /var/run/docker.sock.
// Panics if the resolved container name fails validation (startup misconfiguration).
func NewDefaultDockerRestarter(socketPath string) *DockerRestarter {
	if socketPath == "" {
		socketPath = defaultSocketPath
	}
	name := os.Getenv("LITELLM_CONTAINER_NAME")
	if name == "" {
		name = defaultContainerName
	}
	r, err := NewDockerRestarter(name, socketPath)
	if err != nil {
		panic(fmt.Sprintf("litellmconfig: LITELLM_CONTAINER_NAME is invalid: %v", err))
	}
	return r
}

// Restart sends POST /containers/<name>/restart to the Docker Engine over the
// Unix socket. The call is bounded by a 30-second context deadline.
// Returns a non-nil error when the Docker Engine responds with a non-2xx status
// or the connection fails.
func (r *DockerRestarter) Restart(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, restartTimeout)
	defer cancel()

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", r.socketPath)
		},
	}
	client := &http.Client{Transport: transport}

	// url.PathEscape ensures the container name is safe even if validation is
	// somehow bypassed; the allowlist regex is the primary defence.
	endpoint := "http://localhost/containers/" + url.PathEscape(r.containerName) + "/restart?t=10"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return fmt.Errorf("litellmconfig: docker restart: build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		slog.Error("litellmconfig: docker restart: request failed", "container", r.containerName, "error", err)
		return fmt.Errorf("litellmconfig: docker restart: %w", err)
	}
	defer resp.Body.Close()

	// Docker returns 204 No Content on success.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		slog.Error("litellmconfig: docker restart: unexpected status",
			"container", r.containerName,
			"status", resp.StatusCode,
		)
		return fmt.Errorf("litellmconfig: docker restart: unexpected status %d for container %q", resp.StatusCode, r.containerName)
	}

	slog.Info("litellmconfig: docker restart: success", "container", r.containerName)
	return nil
}
