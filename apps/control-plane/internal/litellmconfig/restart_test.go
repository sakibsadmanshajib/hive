package litellmconfig_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/litellmconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startFakeDockerEngine starts an HTTP server over a Unix socket that records
// request paths and returns the given statusCode. Caller must close the
// returned server and listener.
func startFakeDockerEngine(t *testing.T, socketPath string, statusCode int, received chan<- string) (*http.Server, net.Listener) {
	t.Helper()
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case received <- r.URL.Path:
			default:
			}
			if statusCode == http.StatusNotFound {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(statusCode)
				body, _ := json.Marshal(map[string]string{"message": "No such container"})
				_, _ = w.Write(body)
			} else {
				w.WriteHeader(statusCode)
			}
		}),
	}
	go func() { _ = srv.Serve(listener) }()
	return srv, listener
}

// TestDockerRestarterCallsDockerEngineAPI verifies that DockerRestarter sends
// POST /containers/<name>/restart to a fake Docker Engine over a Unix socket.
func TestDockerRestarterCallsDockerEngineAPI(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "docker.sock")
	received := make(chan string, 1)

	srv, ln := startFakeDockerEngine(t, socketPath, http.StatusNoContent, received)
	defer srv.Close()
	defer ln.Close()

	r, err := litellmconfig.NewDockerRestarter("test-litellm", socketPath)
	require.NoError(t, err)

	require.NoError(t, r.Restart(context.Background()))

	select {
	case req := <-received:
		assert.Contains(t, req, "/containers/test-litellm/restart")
	default:
		t.Fatal("no request received by fake Docker Engine")
	}
}

// TestDockerRestarterPropagatesHTTPError verifies that a non-204 response is
// treated as an error.
func TestDockerRestarterPropagatesHTTPError(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "docker.sock")
	received := make(chan string, 1)

	srv, ln := startFakeDockerEngine(t, socketPath, http.StatusNotFound, received)
	defer srv.Close()
	defer ln.Close()

	r, err := litellmconfig.NewDockerRestarter("no-such-container", socketPath)
	require.NoError(t, err)

	restartErr := r.Restart(context.Background())
	assert.Error(t, restartErr, "non-204 response must be returned as error")
	assert.Contains(t, restartErr.Error(), "404")
}

// TestDockerRestarterRejectsInvalidContainerName verifies that path-injection
// values are rejected at construction time.
func TestDockerRestarterRejectsInvalidContainerName(t *testing.T) {
	cases := []string{
		"",
		"../images/prune",
		"litellm\x00null",
		"name with spaces",
		"name;rm -rf",
	}
	for _, name := range cases {
		_, err := litellmconfig.NewDockerRestarter(name, "/var/run/docker.sock")
		assert.Error(t, err, "expected error for container name %q", name)
	}
}

// TestDockerRestarterUsesEnvVarContainerName verifies DefaultDockerRestarter
// picks up LITELLM_CONTAINER_NAME when set.
func TestDockerRestarterUsesEnvVarContainerName(t *testing.T) {
	t.Setenv("LITELLM_CONTAINER_NAME", "custom-litellm")

	dir := t.TempDir()
	socketPath := filepath.Join(dir, "docker.sock")
	received := make(chan string, 1)

	srv, ln := startFakeDockerEngine(t, socketPath, http.StatusNoContent, received)
	defer srv.Close()
	defer ln.Close()

	r := litellmconfig.NewDefaultDockerRestarter(socketPath)
	require.NoError(t, r.Restart(context.Background()))

	select {
	case path := <-received:
		assert.Contains(t, path, "custom-litellm")
	default:
		t.Fatal("no request received")
	}
}

// TestDockerRestarterDefaultContainerName verifies the default container name
// is "litellm" when env var is not set.
func TestDockerRestarterDefaultContainerName(t *testing.T) {
	os.Unsetenv("LITELLM_CONTAINER_NAME")

	dir := t.TempDir()
	socketPath := filepath.Join(dir, "docker.sock")
	received := make(chan string, 1)

	srv, ln := startFakeDockerEngine(t, socketPath, http.StatusNoContent, received)
	defer srv.Close()
	defer ln.Close()

	r := litellmconfig.NewDefaultDockerRestarter(socketPath)
	require.NoError(t, r.Restart(context.Background()))

	select {
	case path := <-received:
		assert.Equal(t, fmt.Sprintf("/containers/%s/restart", "litellm"), path)
	default:
		t.Fatal("no request received")
	}
}
