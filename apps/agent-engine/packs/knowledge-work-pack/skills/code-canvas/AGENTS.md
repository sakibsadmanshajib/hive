# Code-canvas skill

Claude-Artifacts-style code plus preview canvas. No dedicated Go package:
this skill reuses the exact same publish primitive as `deck-generation`
(`apps/agent-engine/internal/artifactsclient`) with agent-authored HTML/JS
instead of a rendered deck. Issue #300.

## When to use

The task asks the agent to write a small self-contained web page, widget,
visualization, or UI mockup and show a live preview, rather than commit
code to a repository (that is the coding-pack's job, not this skill's).

## How it works

1. Write a single self-contained HTML document to `/workspace` (inline
   `<style>` and `<script>`, no external script/stylesheet references, no
   `fetch`/`XMLHttpRequest` calls — the artifacts CSP serves it with
   `connect-src 'none'`, so any network call from inside the canvas fails
   at render time, not at publish time; do not rely on one working).
2. Publish it:
   `apps/agent-engine/internal/artifactsclient.Client.Create(ctx, bearerJWT, name, html)`
   for a new canvas, or `.AddVersion(ctx, bearerJWT, artifactID, html)` when
   iterating on an existing one in the same task. Read the file back from
   `/workspace` before calling either; do not reconstruct the HTML from
   memory.
3. Return the artifact `URL` to the user.

## Invocation shape (for the panel)

No new task-lifecycle field is introduced by this skill. Prefix the task's
instructions with `Skill: code-canvas` to hint it explicitly (see
`skills/doc-layout/AGENTS.md` for the same `Skill:` tag convention); absent
the tag, the agent recognizes canvas-style requests from their content. As
with `deck-generation`, the panel renders the returned `URL` in a
`sandbox="allow-scripts"` iframe with no `allow-same-origin`.

## Output

An artifact `URL` (and `VersionedURL`) pointing at the rendered canvas.

## Live wiring (env-gated, Wave 3 integration)

Same as `deck-generation`: publishing requires a per-task bearer JWT and
edge-api's base URL from the in-flight engine control-channel work (issue
#305); exercised against a fake edge-api in
`apps/agent-engine/internal/artifactsclient/client_test.go` until that
wiring lands.
