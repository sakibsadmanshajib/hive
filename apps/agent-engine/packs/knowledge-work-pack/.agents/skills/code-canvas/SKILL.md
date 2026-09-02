---
name: code-canvas
description: Write a self-contained HTML page, widget, or visualization into the workspace for preview.
---

# Code-canvas skill

Claude-Artifacts-style code plus preview canvas: a single self-contained
HTML document, authored by the agent, left in the workspace. Issue #300.

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
2. Give the file a descriptive name ending in `.html`, and keep iterating on
   that same file rather than writing a second copy.
3. Tell the user the file name and summarize what the page does. Do not try
   to publish it from in here: publishing runs host side, outside this
   sandbox, and this sandbox has no Go toolchain and no network route to
   edge-api (the same constraint `deck-generation` describes). A canvas is
   delivered as the file in the working folder.

## Invocation shape (for the panel)

No new task-lifecycle field is introduced by this skill. Prefix the task's
instructions with `Skill: code-canvas` to hint it explicitly (see
`.agents/skills/doc-layout/SKILL.md` for the same `Skill:` tag convention); absent
the tag, the agent recognizes canvas-style requests from their content. When a
canvas is eventually published host side, the panel is expected to render it
in a `sandbox="allow-scripts"` iframe with no `allow-same-origin`, as it does
for a deck.

## Output

One self-contained `.html` file in the workspace, named in the reply.

## Not wired yet (host side, not a sandbox concern)

There is no host-side publication step for a canvas today. `deck-generation`
has one because the engine watches for a single well-known manifest path and
renders and publishes it after the task
(`apps/agent-engine/internal/engine`'s deck flow, using
`apps/agent-engine/internal/artifactsclient`); nothing does the equivalent
for arbitrary agent-authored HTML. Until that exists, the agent must not
promise the user an artifact URL.
