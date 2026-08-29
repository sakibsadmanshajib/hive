---
name: Worktree Compose Stack
description: Use before running any `docker compose` command from a worktree checkout of this repo, and when a full local stack won't start at all (control-plane exits at boot on `storage unavailable`, or warns `database unreachable`). Covers the per-worktree compose-project-name collision and the current empty-`.env` blocker that makes a full local stack impossible from this sandbox, with the fallback commands agents actually use.
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

**Run it again, with no flag, every time you create or copy the repo-root
`.env`.** Once at worktree creation is not enough.
`scripts/set-compose-project-name.sh:103-107` writes `deploy/docker/.env`
unconditionally but touches `<repo-root>/.env` **only if that file already
exists**:

```bash
upsert_line "$compose_dir/.env"

if [[ -f "$repo_root/.env" ]]; then
  upsert_line "$repo_root/.env"
fi
```

A worktree namespaced before its `.env` was copied in therefore ends up with
the namespace on `deploy/docker/.env` and the default `hive` project name on
the repo-root `.env`, and every `--env-file ../../.env` command shape then
recreates another checkout's containers exactly as if the script had never run.
That is a live container collision produced by a script you did run.

Then verify there's no live collision before trusting a run:

```bash
scripts/set-compose-project-name.sh --check
```

Note what `--check` does and does not cover: it inspects `docker ps -a` for
containers labelled with this worktree's derived project name whose working
directory is some other checkout, and exits 1 naming that directory. It does
**not** read either `.env`, so it cannot tell you the repo-root one is missing
the variable. Confirm that yourself:

```bash
grep -H COMPOSE_PROJECT_NAME .env deploy/docker/.env
```

## The full local stack currently cannot start on this machine

Verified 2026-08-29. **Do not grep `.env` for a Supabase hostname to confirm
this. There is no longer one to find, and its absence is not good news.**

The blocker did not go away when issue #1254 closed. It changed shape. The
keys are still there and they are now **empty**: `SUPABASE_URL`,
`SUPABASE_DB_URL`, `S3_ENDPOINT` and `NEXT_PUBLIC_SUPABASE_URL` are all
present in this machine's `.env` with a zero-length value. That is a
different failure from the one this section used to describe, it produces a
different error message, and it needs a different fix, so an agent that reads
the old text, greps for the old hostname, finds nothing and concludes the
stack should now work is worse off than one who read nothing.

What the two shapes look like, so you can tell which you are in:

- **Then** (a hostname that resolves to nothing, issue #1254, now closed):
  `control-plane` came up and logged `database unreachable after 6
  attempt(s)`, a generic connectivity warning that named no cause.
- **Now** (empty values): `control-plane` never reaches the database check.
  It exits at boot, because `loadStorageConfigFromEnv` requires the S3 names
  to be non-empty and `log.Fatalf`s otherwise:
  `storage unavailable: missing S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY,
  S3_REGION`. `edge-api` refuses for the same reason. This is a configuration
  failure, not a network one, and no amount of waiting or retrying helps.

Repointing `.env` at the real stack is not available either, and that part is
unchanged and is deliberate. `deploy/docker/Caddyfile.supabase` serves
`/rest/v1` and `/storage/v1` on the in-network listener only, and states that
ports 80 and 443 are the in-network surface and must never be exposed; the
public listener carries `/auth/v1` minus its admin routes and nothing else.
There is no public hostname for `caddy-supabase`, by design, so no value you
can write into `.env` from a sandbox reaches the self-hosted data plane.

Practical consequence, unchanged: no full local stack, no local sign-in, no
local E2E run from this environment. What changed is only the error you will
see on the way to learning that. Use the fallbacks below instead of trying to
make the stack come up.

If you need a real Supabase-shaped surface rather than a fallback, the repo
already has one that does not depend on `.env` at all:
`scripts/ci-supabase-stack.sh` boots GoTrue, PostgREST and a gateway on a
throwaway Postgres, and `scripts/ci-object-store.sh` boots a real Storage API
next to it. That pair is what ci.yml's own jobs use, and it works from a
sandbox because it depends on nothing outside the machine.

This same deleted project is still referenced in more than one place. The
CI-side instance is the `S3_ENDPOINT` repository secret, last written
2026-04-21, which is issue #1324. If you find a third, say so rather than
working around it locally.

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
page/component tree server-side with React's `renderToStaticMarkup`, mocking
the same seam the unit tests already mock
(`vi.mock('@/lib/control-plane/client', ...)`), write the HTML through a
mounted volume alongside the real `next build` output's compiled Tailwind CSS
chunk (`.next/static/chunks/*.css`, also copied out via a mounted volume),
then screenshot the static HTML file in a real browser (chrome-devtools MCP
or Playwright). This is not a mock-only screenshot: it renders the actual
component tree against the actual compiled styles, just without a live
backend. Full worked example (from PR #1243, merged; the file lives on `main`, not on
the PR's own branch, which is already deleted per this repo's
squash-and-delete-branch merge policy):
`docs/proof/console-overview-cards-and-try-in-chat/capture-log.md`.

Do not spend time debugging this as a docker-networking or timeout problem
before checking issue #1254's status; the symptom (generic DB-unreachable
warning) actively points the wrong direction.
