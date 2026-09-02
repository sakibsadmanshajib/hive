package engine

// Event/files surface for the launcher daemon: reads the live session's
// sandbox event store through its control client and lists the workspace
// bind mount from the host directly. Once a session is reaped (issue #1206),
// both instead serve the snapshot reap() captured immediately before it tore
// the control socket and /workspace bind mount down — the same pattern
// Status already uses via replayOf/terminal, just for the tail data too.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/controlclient"
)

// Events returns the session's normalized sandbox events: the live pull for
// an active session, or reap's captured snapshot for one already terminal.
func (e *SandboxEngine) Events(ctx context.Context, sessionRef string) ([]controlclient.Event, error) {
	sess, id, err := e.lookup(sessionRef)
	if err != nil {
		return nil, err
	}
	if cached, reaped := e.finalEventsOf(sess); reaped {
		return cached, nil
	}
	return sess.client.SearchEvents(ctx, id)
}

// Files lists the session workspace directory (top level, name/size/mtime
// only): the live listing for an active session, or reap's captured snapshot
// for one already terminal.
func (e *SandboxEngine) Files(_ context.Context, sessionRef string) ([]controlclient.WorkspaceFile, error) {
	sess, _, err := e.lookup(sessionRef)
	if err != nil {
		return nil, err
	}
	if cached, reaped := e.finalFilesOf(sess); reaped {
		return cached, nil
	}
	return listWorkspaceFiles(sess.workingDir, sess.packFiles)
}

// finalEventsOf and finalFilesOf return sess's reap-time snapshot plus
// whether sess has been reaped at all — the caller's signal to use it rather
// than dial a control socket or read a bind mount that may already be gone.
// Guarded by e.mu, same as sess.reaped and sess.terminal.
func (e *SandboxEngine) finalEventsOf(sess *session) ([]controlclient.Event, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return sess.finalEvents, sess.reaped
}

func (e *SandboxEngine) finalFilesOf(sess *session) ([]controlclient.WorkspaceFile, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return sess.finalFiles, sess.reaped
}

// listWorkspaceFiles lists dir's top level (name/size/mtime only), minus the
// pack entries planted there at launch (issue #1360): the panel's working
// folder shows what the task produced, and the pack is what it started with.
// Shared by the live-session Files() path and reap's final-snapshot capture.
//
// Only an entry that still carries its planted timestamp is hidden. Filtering
// on the name alone would let the sandboxed agent conceal a file simply by
// calling it AGENTS.md, and the pack now carries untrusted document content
// into the model's context, so "an instruction told it to write there" is a
// reachable state rather than a hypothetical one.
//
// ponytail: no recursion; add a walk when a panel needs nested paths.
func listWorkspaceFiles(dir string, packFiles map[string]time.Time) ([]controlclient.WorkspaceFile, error) {
	entries, err := os.ReadDir(dir)
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
		if planted, ok := packFiles[entry.Name()]; ok && info.ModTime().Equal(planted) {
			continue
		}
		out = append(out, controlclient.WorkspaceFile{
			Name:    entry.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	return out, nil
}
