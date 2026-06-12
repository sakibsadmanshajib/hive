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

// TestDockerRestarterCallsDockerEngineAPI verifies that DockerRestarter sends
// POST /containers/<name>/restart to a fake Docker Engine over a Unix socket.
func TestDockerRestarterCallsDockerEngineAPI(t *testing.T) {
	// Create a temp Unix socket.
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "docker.sock")

	requestReceived := make(chan string, 1)

	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer listener.Close()

	// Fake Docker Engine HTTP server over the Unix socket.
	fakeSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestReceived <- r.URL.Path + "?" + r.URL.RawQuery
			w.WriteHeader(http.StatusNoContent) // Docker returns 204 on success.
		}),
	}
	go func() { _ = fakeSrv.Serve(listener) }()
	defer fakeSrv.Close()

	r := litellmconfig.NewDockerRestarter("test-litellm", socketPath)
	err = r.Restart(context.Background())
	require.NoError(t, err)

	select {
	case req := <-requestReceived:
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

	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer listener.Close()

	fakeSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			body, _ := json.Marshal(map[string]string{"message": "No such container"})
			_, _ = w.Write(body)
		}),
	}
	go func() { _ = fakeSrv.Serve(listener) }()
	defer fakeSrv.Close()

	r := litellmconfig.NewDockerRestarter("no-such-container", socketPath)
	err = r.Restart(context.Background())
	assert.Error(t, err, "non-204 response must be returned as error")
	assert.Contains(t, err.Error(), "404")
}

// TestDockerRestarterUsesEnvVarContainerName verifies DefaultDockerRestarter
// picks up LITELLM_CONTAINER_NAME when set.
func TestDockerRestarterUsesEnvVarContainerName(t *testing.T) {
	t.Setenv("LITELLM_CONTAINER_NAME", "custom-litellm")

	dir := t.TempDir()
	socketPath := filepath.Join(dir, "docker.sock")

	requestReceived := make(chan string, 1)

	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer listener.Close()

	fakeSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestReceived <- r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	go func() { _ = fakeSrv.Serve(listener) }()
	defer fakeSrv.Close()

	r := litellmconfig.NewDefaultDockerRestarter(socketPath)
	err = r.Restart(context.Background())
	require.NoError(t, err)

	select {
	case path := <-requestReceived:
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

	requestReceived := make(chan string, 1)

	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer listener.Close()

	fakeSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestReceived <- r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	go func() { _ = fakeSrv.Serve(listener) }()
	defer fakeSrv.Close()

	r := litellmconfig.NewDefaultDockerRestarter(socketPath)
	err = r.Restart(context.Background())
	require.NoError(t, err)

	select {
	case path := <-requestReceived:
		assert.Equal(t, fmt.Sprintf("/containers/%s/restart", "litellm"), path)
	default:
		t.Fatal("no request received")
	}
}
