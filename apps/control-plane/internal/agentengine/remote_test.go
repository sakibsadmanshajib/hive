package agentengine

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
)

// TestRemoteErrorDoesNotLeakDaemonDetail is the provider-blind boundary guard:
// the launcher daemon's error text names sandbox paths, egress hosts and the
// model provider, and none of that may travel back into control-plane's own
// error values, which downstream code may surface.
func TestRemoteErrorDoesNotLeakDaemonDetail(t *testing.T) {
	const detail = "engine: start conversation: dial acme-inference-provider.example: refused"

	socketPath := filepath.Join(t.TempDir(), "engine.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":"`+detail+`"}`)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	// The detail is logged, not returned; keep it out of the test output.
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	_, _, _, err = NewRemote(socketPath, "").Status(context.Background(), "session-ref")
	if err == nil {
		t.Fatal("expected an error for a 502 daemon response")
	}
	if strings.Contains(err.Error(), "acme-inference-provider") || strings.Contains(err.Error(), detail) {
		t.Fatalf("daemon detail crossed the boundary: %v", err)
	}
	if !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "/status") {
		t.Fatalf("error should still identify the operation and status, got: %v", err)
	}
}

// TestRemoteStatus404MapsToEngineSessionGone proves the launcher's 404
// response (apps/agent-engine/cmd/agent-engine/serve.go maps
// engineapi.ErrUnknownSession onto it) becomes agenttask.ErrEngineSessionGone
// on this side, so the poller can tell "this session can never answer again"
// apart from a transient failure — the root-cause fix for a lost session
// polling forever and degrading the shared poll cadence for every tenant.
func TestRemoteStatus404MapsToEngineSessionGone(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "engine.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"engine: unknown session reference: session-ref"}`)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	_, _, _, err = NewRemote(socketPath, "").Status(context.Background(), "session-ref")
	if err == nil {
		t.Fatal("expected an error for a 404 daemon response")
	}
	if !errors.Is(err, agenttask.ErrEngineSessionGone) {
		t.Fatalf("expected errors.Is(err, agenttask.ErrEngineSessionGone), got: %v", err)
	}
	if !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "/status") {
		t.Fatalf("error should still identify the operation and status, got: %v", err)
	}
}
