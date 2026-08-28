---
name: Worktree Compose Stack
description: Use before running any `docker compose` command from a worktree checkout of this repo, and when a full local stack won't start at all (control-plane can't reach a database, `.env` points somewhere dead). Covers the per-worktree compose-project-name collision and the current dead-Supabase-host blocker, with the fallback commands agents actually use.
---

# Worktree Compose Stack

## Namespace your worktree before running compose

`docker compose` keys container identity on the compose project name
(`COMPOSE_PROJECT_NAME`, falling back to the `name:` in
`docker-compose.yml`, which is `hive`). Every worktree checkout of this repo
shares that same fallback, so `docker compose run --build web-console ...` in
one worktree can recreate `hive-control-plane-1` that actually belongs to a
different worktree's running stack (issue #1242). Run this once per worktree
before any compose command:

```bash
scripts/set-compose-project-name.sh
```

It writes `COMPOSE_PROJECT_NAME=hive-<slug>-<hash>` into both
`deploy/docker/.env` (compose's default `.env` discovery, used by commands
that pass no `--env-file`, e.g. the `docker compose run --build web-console
...` build/test commands) and `<repo-root>/.env` if it already exists (read
via `--env-file ../../.env` by every other documented profile command). Both
files matter; an explicit `--env-file` argument fully replaces compose's
default `.env` discovery, so one alone does not cover every command shape in
this repo's `CLAUDE.md`.

It is a safe no-op on the canonical checkout: if the worktree root's basename
is literally `hive` (the demo box, CI, or a plain single-checkout dev setup),
it writes nothing and the stack keeps the `hive` project name it has always
had.

Verify there's no live collision before trusting a run, without writing
anything:

```bash
scripts/set-compose-project-name.sh --check
```

## The full local stack currently cannot start on this machine

This machine's `.env` (and any worktree `.env` copied from it) still points
at a Supabase Cloud project that no longer exists (issue #1254):
`SUPABASE_URL=https://yimgflllgdsbcibnaxqe.supabase.co` and siblings resolve
NXDOMAIN, not a timeout or auth error — the project itself is gone since the
self-hosted cutover. `docker compose --profile local up` brings
`control-plane` up, but it logs a generic-looking connectivity warning
(`database unreachable after 6 attempt(s)`) that gives no hint the real cause
is a deleted upstream project, and easily sends you chasing a transient
network blip instead. There is also no way to reach the self-hosted
replacement from outside the box's internal docker network by design (no
public hostname for `caddy-supabase`), so this isn't fixable by editing
`.env` to point somewhere else reachable from a sandbox.

Practical consequence: no full local stack, no local sign-in, no local E2E
run, from this environment, until `.env` is repointed at the self-hosted
stack or the sandbox gains a reachable route to it. Check whether #1254 is
still open before assuming this; if it's closed, this section is stale.

### Known-good fallbacks (from PR #1243)

For web-console build/test verification, which needs no DB access at build
or test time despite `depends_on: control-plane` in the compose file:

```bash
docker compose run --no-deps --build web-console npm run build
docker compose run --no-deps --build web-console npm run test:unit
```

`--no-deps` skips the `control-plane` dependency compose would otherwise
pull in unconditionally, even though this target never calls it.

For visual proof when no full stack is reachable at all: render the real
page/component tree server-side with React's `renderToStaticMarkup`,
mocking the same seam the unit tests already mock (`vi.mock('@/lib/control-
plane/client', ...)`), write the HTML through a mounted volume alongside the
real `next build` output's compiled Tailwind CSS chunk
(`.next/static/chunks/*.css`, also copied out via a mounted volume), then
screenshot the static HTML file in a real browser (chrome-devtools MCP or
Playwright). This is not a mock-only screenshot: it renders the actual
component tree against the actual compiled styles, just without a live
backend. Full worked example: `docs/proof/console-overview-cards-and-try-
in-chat/capture-log.md` on that PR's branch.

Do not spend time debugging this as a docker-networking or timeout problem
before checking issue #1254's status; the symptom (generic DB-unreachable
warning) actively points the wrong direction.
