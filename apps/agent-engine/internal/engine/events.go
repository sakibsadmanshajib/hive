package engine

// Event/files surface for the launcher daemon: reads the live session's
// sandbox event store through its control client and lists the workspace
// bind mount from the host directly.

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/controlclient"
)

// Events returns the session's normalized sandbox events. A reaped session's
// control socket is gone, so the call errors; the syncer treats that like any
// other per-task failure and the task's row is terminal by then anyway.
func (e *SandboxEngine) Events(ctx context.Context, sessionRef string) ([]controlclient.Event, error) {
	sess, id, err := e.lookup(sessionRef)
	if err != nil {
		return nil, err
	}
	return sess.client.SearchEvents(ctx, id)
}

// Files lists the session workspace directory (top level, name/size/mtime
// only), reading the host bind mount directly.
//
// ponytail: no recursion; add a walk when a panel needs nested paths.
func (e *SandboxEngine) Files(_ context.Context, sessionRef string) ([]controlclient.WorkspaceFile, error) {
	sess, _, err := e.lookup(sessionRef)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(sess.workingDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// A reaped session's working dir is already deleted; that is a
			// known state, not a failure to log loudly.
			return nil, nil
		}
		return nil, fmt.Errorf("engine: list workspace: %w", err)
	}
	out := make([]controlclient.WorkspaceFile, 0, len(entries))
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue // raced a deletion mid-listing; skip, never fail the batch
		}
		out = append(out, controlclient.WorkspaceFile{
			Name:    entry.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	return out, nil
}
