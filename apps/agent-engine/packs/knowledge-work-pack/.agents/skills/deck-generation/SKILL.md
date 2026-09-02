---
name: deck-generation
description: Outline a slide deck or presentation from the task content and publish it as an artifact.
---

# Deck-generation skill

Template-driven slide deck generation. No LLM call of its own beyond the
agent's normal reasoning: the agent decides slide titles/bullets from the
task content and writes that as a small JSON manifest; the host-side engine
(outside this sandbox) renders and publishes it. Folds in the "Claude
Design" deck-generation ask (blueprint D10). Issue #300.

## When to use

The task asks for a slide deck, a presentation, or a deck-style summary of
some content (a document, a set of notes, a plan).

## How it works

This sandbox has no Go toolchain and no network route to edge-api (only the
tenant's egress-policy allowlist, `apps/agent-engine/internal/egressproxy`),
so it cannot call `deckgen.Render` or `artifactsclient.Client` directly —
both are Go packages that only ever run in the agent-engine host process.
Instead:

1. Outline the deck as a title plus an ordered list of slides, each with a
   slide title and bullet points, from the task's content.
2. Write that outline as JSON to `.hive/deck.json` under `/workspace`
   (create the `.hive/` directory if it does not exist):
   ```json
   {
     "title": "Deck title",
     "slides": [
       {"title": "Slide 1 title", "bullets": ["point one", "point two"]}
     ]
   }
   ```
   Add `"artifact_id": "<id>"` at the top level only when the task is
   explicitly regenerating a deck it (or a prior task) already published —
   the id is whatever `/artifacts/{id}` path the earlier task's result
   linked to. Omit it for a new deck.
3. That is the whole of this skill's job. Once the task's conversation
   finishes, the agent-engine host process
   (`apps/agent-engine/internal/engine.SandboxEngine.Status`, method
   `publishDeckArtifact`) reads exactly this one file — nothing else under
   `/workspace` is treated as this skill's output — renders it with
   `apps/agent-engine/internal/deckgen.Render` (self-contained HTML: inline
   CSS, inline arrow-key/click navigation JS, no external script or
   stylesheet reference, required by the artifacts CSP,
   `apps/edge-api/internal/artifacts`; all slide content is HTML-escaped by
   `html/template`, so task- or tenant-supplied text is safe to write
   straight through), and publishes it via
   `apps/agent-engine/internal/artifactsclient.Client` (`Create` for a new
   deck, `AddVersion` when `artifact_id` was set) authenticated as the
   task's own user — never as an internal service identity.

## Invocation shape (for the panel)

No new task-lifecycle field is introduced by this skill. Prefix the task's
instructions with `Skill: deck-generation` to hint it explicitly (see
`.agents/skills/doc-layout/SKILL.md` for the same `Skill:` tag convention); absent
the tag, the agent recognizes deck-request tasks from their content. The
panel should render the returned artifact URL in a sandboxed iframe
(`sandbox="allow-scripts"`, no `allow-same-origin` — see
`apps/edge-api/internal/artifacts`'s open risk note on this exact
requirement) for the live preview.

## Output

The task's `result_summary_ref` becomes the published artifact's stable URL
(`/artifacts/{id}`, survives redeploys) once publishing succeeds. If nothing
was published for any reason (no manifest was written, the manifest was
malformed or oversized, or edge-api rejected or was unreachable),
`result_summary_ref` stays the agent's own final-response text instead —
never a broken link.

## Live wiring

Publishing needs a per-task bearer JWT (the task's own user's JWT, forwarded
from edge-api's task-create request through control-plane; empty for an
API-key-authenticated task create, which just skips publishing) and
`EDGE_API_URL` configured on the agent-engine host daemon (optional;
unset disables publishing outright, see root `CLAUDE.md`'s agent-engine
section). `Create`/`AddVersion` are exercised against a fake edge-api in
`apps/agent-engine/internal/artifactsclient/client_test.go`; the manifest
read/render/publish sequence itself is covered in
`apps/agent-engine/internal/engine/engine_test.go`
(`TestSandboxEngine_Status_PublishesDeckManifestAsArtifactURL` and
neighbors).
