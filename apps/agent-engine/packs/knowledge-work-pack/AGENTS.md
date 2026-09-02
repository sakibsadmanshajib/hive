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
  - `.agents/skills/doc-layout/SKILL.md` — contract/PDF page understanding via the
    `route-doc-vlm` vision route.
  - `.agents/skills/deck-generation/SKILL.md` — self-contained HTML slide deck
    generation, published through the artifacts API.
  - `.agents/skills/code-canvas/SKILL.md` — self-contained HTML/JS code
    preview, left in the workspace as a file; there is no host-side publish
    step for it yet, so never promise the user a URL for one.
- When the task's instructions open with a `Skill: <name>` line, read that
  skill's `.agents/skills/<name>/SKILL.md` and follow it, before deciding on
  any other approach. Absent the tag, pick a skill from its description
  above when the task matches one.
- Run arbitrary shell commands where a knowledge-work task needs them:
  document conversion tools, template renderers, arbitrary build/test
  commands are not excluded by pack type.

## Constraints

- All outbound network access is bound by the tenant/user's effective
  egress-policy allowlist (apps/control-plane/internal/egress, issue #308).
  A request to any host outside that allowlist fails closed.
- The Docker socket is never mounted into or reachable from this sandbox
  (security spike #307 rows 8/9).
