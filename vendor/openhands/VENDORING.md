# Vendored OpenHands source (issue #305/#308)

## Source

- Upstream: https://github.com/OpenHands/software-agent-sdk (MIT, no
  `enterprise/` directory — the `enterprise/`-carve-out license split lives in
  the separate `OpenHands/OpenHands` monorepo, which this vendoring does not
  pull from).
- Commit: `51c102b9c0348bbdd4e6a84b1ac4199e0d77f827`
- Commit date: 2026-07-15T21:16:12+02:00
- Fetched: 2026-07-16, via a partial `git clone --filter=blob:none` plus
  `sparse-checkout` (no full history, no unrelated top-level directories).

### Why this repo and not `OpenHands/OpenHands`

The blueprint (`plan-2026-07-15-agent-subsystem-blueprint.md`, Step 2.2)
names "OpenHands (MIT, excluding `enterprise/`)" as the vendoring target,
written against an OpenHands architecture that has since split. As of this
commit, `OpenHands/OpenHands`'s own `openhands/` package is the OpenHands
Cloud SaaS server (`analytics/`, `app_server/`, `db/`, `server/`) — it no
longer contains the agent execution loop, tools, or workspace/sandbox
abstraction. That code now lives in this separate repo as three PyPI
packages (`openhands-sdk`, `openhands-tools`, `openhands-agent-server`) plus
`openhands-workspace`, which is the actual `workspace_factory` plugin point
the blueprint's Step 4.2 (desktop sandbox backends) refers to: it already
ships `apptainer/`, `docker/`, `cloud/`, and `remote_api/` workspace
backends side by side. This repo carries no `enterprise/` directory at all
(confirmed: `contents/enterprise` 404s), so the blueprint's "excluding
enterprise/" instruction is satisfied trivially rather than by exclusion.

## What was vendored

Only the four installable packages, not the full monorepo:

- `openhands-sdk/` — the agent/tool/LLM-routing core.
- `openhands-tools/` — first-party tools (bash, file editor, browser, ...).
- `openhands-agent-server/` — the REST/WebSocket server that runs inside the
  sandbox and that a workspace backend health-checks and talks to.
- `openhands-workspace/` — the pluggable sandbox/workspace backends,
  including `apptainer/workspace.py` (patched, see below).

Deliberately NOT vendored: `frontend/`, `openhands-ui/` (this repo has no
web frontend of its own; the OWUI-driven panel is Wave 3), `tests/`,
`examples/`, `.github/`, `scripts/`, `.devcontainer/` — none of these are
needed to install and run the agent-server headless inside a sandbox.

## Patches applied (all marked `HIVE PATCH` inline)

`openhands-workspace/openhands/workspace/apptainer/workspace.py`:

1. `container_opts` now unconditionally starts with `["--pid",
   "--containall"]`, not configurable off. Upstream's default (`--fakeroot
   --compat` only) shares the host PID namespace — security spike #307
   (implementation condition 1) proved this the exact gap. Hive's own
   launcher (`apps/agent-engine/internal/sandbox`) does not call this class;
   it independently constructs and validates the same `apptainer run`
   invocation. This patch is defense in depth for any future code path that
   calls `ApptainerWorkspace` directly.
2. Any bind-mount spec (`extra_bind_mounts` or the
   `OPENHANDS_APPTAINER_EXTRA_BINDS` env var) referencing the Docker socket
   path or `DOCKER_HOST` now raises `RuntimeError` instead of being silently
   accepted (spike #307 rows 8/9).

No other files in this vendor tree are patched.

## Updating this vendor copy

1. Re-run the same partial clone against the new upstream commit:
   `git clone --depth 1 --filter=blob:none --no-checkout
   https://github.com/OpenHands/software-agent-sdk.git` then
   `git sparse-checkout set openhands-sdk openhands-tools
   openhands-agent-server openhands-workspace` then `git checkout main`.
2. Copy the four package directories plus `LICENSE`/`README.md` over this
   directory.
3. Re-apply both patches above to
   `openhands-workspace/openhands/workspace/apptainer/workspace.py` (diff
   against git history in this repo to regenerate the patch if upstream
   changed the surrounding code).
4. Update the commit SHA/date at the top of this file.
