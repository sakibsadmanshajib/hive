# Coding Pack

This pack runs inside the Apptainer rootless sandbox (apps/agent-engine/internal/sandbox)
alongside the knowledge-work pack, at the identical sandbox trust tier: both
packs may run arbitrary shell, build, and test commands inside the
container. Nothing about this pack config grants it broader sandbox
permissions than the knowledge-work pack; the difference between packs is
task framing and default tooling emphasis only.

## Scope

- Read, write, and refactor code in the mounted `/workspace` directory.
- Run arbitrary shell commands: package managers, linters, formatters,
  compilers, build systems.
- Run the project's build command and test suite, and iterate on failures.
- Use version control (git) inside the workspace; it is not pushed anywhere
  from inside the sandbox — that stays a caller-side (edge-api/OWUI) concern.

## Constraints

- All outbound network access is bound by the tenant/user's effective
  egress-policy allowlist (apps/control-plane/internal/egress, issue #308).
  A request to any host outside that allowlist fails closed.
- The Docker socket is never mounted into or reachable from this sandbox
  (security spike #307 rows 8/9).
