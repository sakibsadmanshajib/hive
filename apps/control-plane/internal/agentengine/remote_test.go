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

// TestRemoteLaunch404DoesNotMapToEngineSessionGone is the negative case the
// path-scoping fix above exists for: net/http.ServeMux answers 404 for ANY
// path it has no handler for, not just the launcher's own deliberate
// unknown-session case, and post also serves /launch. A control-plane build
// calling an endpoint a mismatched launcher binary does not have (a routing
// miss, not "this session is gone") must not be misread as
// agenttask.ErrEngineSessionGone — Launch has no session reference to be
// gone in the first place.
func TestRemoteLaunch404DoesNotMapToEngineSessionGone(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "engine.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	// A bare http.NotFoundHandler models a routing miss: every path, every
	// method, 404 — not a launcher that specifically recognizes
	// engineapi.ErrUnknownSession.
	srv := &http.Server{Handler: http.NotFoundHandler()}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	_, err = NewRemote(socketPath, "").Launch(context.Background(), agenttask.Task{})
	if err == nil {
		t.Fatal("expected an error for a 404 daemon response")
	}
	if errors.Is(err, agenttask.ErrEngineSessionGone) {
		t.Fatalf("a /launch 404 (a routing miss, no session involved) must not map to ErrEngineSessionGone: %v", err)
	}
}

// TestRemoteTransportFailureDoesNotMapToEngineSessionGone proves a dial
// failure (the launcher is not running at all) never becomes
// agenttask.ErrEngineSessionGone: that error is returned above the status
// code branch entirely, so treating "unreachable" the same as "reachable and
// says gone" cannot happen here. This is the discrimination the earlier
// transport-collapsed-into-401 defect was missing.
func TestRemoteTransportFailureDoesNotMapToEngineSessionGone(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "engine.sock")
	// Deliberately never listened on: dialing it fails immediately.

	_, _, _, err := NewRemote(socketPath, "").Status(context.Background(), "session-ref")
	if err == nil {
		t.Fatal("expected a dial error against a socket nothing is listening on")
	}
	if errors.Is(err, agenttask.ErrEngineSessionGone) {
		t.Fatalf("a transport failure must not map to ErrEngineSessionGone: %v", err)
	}
}
