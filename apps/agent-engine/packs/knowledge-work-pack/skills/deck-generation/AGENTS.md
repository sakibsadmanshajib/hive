# Deck-generation skill

Template-driven slide deck generation. No LLM call of its own beyond the
agent's normal reasoning: the agent decides slide titles/bullets from the
task content, then this skill turns that content into a self-contained
HTML deck via `apps/agent-engine/internal/deckgen`, published through the
artifacts API. Folds in the "Claude Design" deck-generation ask (blueprint
D10). Issue #300.

## When to use

The task asks for a slide deck, a presentation, or a deck-style summary of
some content (a document, a set of notes, a plan).

## How it works

1. Outline the deck as a title plus an ordered list of slides, each with a
   slide title and bullet points, from the task's content.
2. Render it: `apps/agent-engine/internal/deckgen.Render(deck)` takes a
   `Deck{Title, Slides []Slide{Title, Bullets []string}}` and returns one
   self-contained HTML string (inline CSS, inline arrow-key/click
   navigation JS, no external script or stylesheet references — required by
   the artifacts CSP, `apps/edge-api/internal/artifacts`). All slide
   content is HTML-escaped by `html/template`, so it is safe to pass
   task-supplied or tenant-supplied text straight through.
3. Publish the HTML as an artifact:
   `apps/agent-engine/internal/artifactsclient.Client.Create(ctx, bearerJWT, name, html)`
   returns `{ID, Version, URL, VersionedURL}`. `URL` is the stable,
   redeploy-surviving link; hand that back to the user, not `VersionedURL`,
   unless the task is explicitly regenerating a prior deck (then use
   `AddVersion` against the existing artifact ID instead of `Create`).

## Invocation shape (for the panel)

No new task-lifecycle field is introduced by this skill. Prefix the task's
instructions with `Skill: deck-generation` to hint it explicitly (see
`skills/doc-layout/AGENTS.md` for the same `Skill:` tag convention); absent
the tag, the agent recognizes deck-request tasks from their content. The
panel should render the returned artifact `URL` in a sandboxed iframe
(`sandbox="allow-scripts"`, no `allow-same-origin` — see
`apps/edge-api/internal/artifacts`'s open risk note on this exact
requirement) for the live preview.

## Output

An artifact `URL` (and `VersionedURL`) pointing at the rendered deck.

## Live wiring (env-gated, Wave 3 integration)

Publishing requires a per-task bearer JWT and edge-api's base URL, both
supplied by the in-flight engine control-channel work (issue #305); this
skill's `Create`/`AddVersion` calls are exercised against a fake edge-api in
`apps/agent-engine/internal/artifactsclient/client_test.go` until that
wiring lands.
