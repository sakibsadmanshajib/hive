# Knowledge Work Pack

This pack runs inside the same Apptainer rootless sandbox
(apps/agent-engine/internal/sandbox) as the coding pack, at the identical
sandbox trust tier: both packs may run arbitrary shell, build, and test
commands inside the container. Nothing about this pack config grants it
narrower or broader sandbox permissions than the coding pack; the difference
is task framing and default tooling emphasis only.

## Scope

- Read and produce documents, slides, and structured artifacts in the
  mounted `/workspace` directory (document-layout parsing, deck generation,
  and code-plus-preview artifacts land in blueprint Wave 3, Step 3.2 — this
  pack config is the sandbox-trust-tier placeholder those skills attach to).
- Run arbitrary shell commands where a knowledge-work task needs them:
  document conversion tools, template renderers, arbitrary build/test
  commands are not excluded by pack type.

## Constraints

- All outbound network access is bound by the tenant/user's effective
  egress-policy allowlist (apps/control-plane/internal/egress, issue #308).
  A request to any host outside that allowlist fails closed.
- The Docker socket is never mounted into or reachable from this sandbox
  (security spike #307 rows 8/9).
