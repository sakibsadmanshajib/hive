package agentengine

import (
	"context"
	"errors"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/engineapi"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
)

// TestEngineStatusUnknownSessionMapsToEngineSessionGone is the in-process
// arm's half of the same fix Remote.post carries for the socket arm
// (remote_test.go's TestRemoteStatus404MapsToEngineSessionGone): a session
// reference the wrapped SandboxEngine has no memory of must map onto
// agenttask.ErrEngineSessionGone here too, so the poller's "this can never
// answer again" handling does not depend on which agenttask.Engine arm is
// wired in. Constructing a zero-value engineapi.Config is enough: lookup
// against an empty in-memory session map needs no sandbox, socket, or
// external process.
func TestEngineStatusUnknownSessionMapsToEngineSessionGone(t *testing.T) {
	sandbox := engineapi.New(engineapi.Config{})
	e := New(sandbox)

	_, _, _, err := e.Status(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("expected an error for an unknown session reference")
	}
	if !errors.Is(err, agenttask.ErrEngineSessionGone) {
		t.Fatalf("expected errors.Is(err, agenttask.ErrEngineSessionGone), got: %v", err)
	}
}
