# Knowledge Work Pack

This pack runs inside the same Apptainer rootless sandbox
(apps/agent-engine/internal/sandbox) as the coding pack, at the identical
sandbox trust tier: both packs may run arbitrary shell, build, and test
commands inside the container. Nothing about this pack config grants it
narrower or broader sandbox permissions than the coding pack; the difference
is task framing and default tooling emphasis only.

## Scope

- Read and produce documents, slides, and structured artifacts in the
  mounted `/workspace` directory. Three skills ship with this pack
  (blueprint Wave 3, Step 3.2, issue #300); check whether the task at hand
  matches one before improvising a from-scratch approach:
  - `skills/doc-layout/AGENTS.md` — contract/PDF page understanding via the
    `route-doc-vlm` vision route.
  - `skills/deck-generation/AGENTS.md` — self-contained HTML slide deck
    generation, published through the artifacts API.
  - `skills/code-canvas/AGENTS.md` — self-contained HTML/JS code preview,
    published through the artifacts API.
- Run arbitrary shell commands where a knowledge-work task needs them:
  document conversion tools, template renderers, arbitrary build/test
  commands are not excluded by pack type.

## Constraints

- All outbound network access is bound by the tenant/user's effective
  egress-policy allowlist (apps/control-plane/internal/egress, issue #308).
  A request to any host outside that allowlist fails closed.
- The Docker socket is never mounted into or reachable from this sandbox
  (security spike #307 rows 8/9).
