---
name: Open WebUI Fork Edits
description: Use before changing anything in the chat front end (`vendor/open-webui`, `deploy/docker/owui-patches/`, `deploy/docker/Caddyfile.owui`), and when a change to the vendored tree appears to have no effect on the running chat. Explains why a backend edit under `vendor/open-webui` is inert, which of the three layers a given change belongs in, and how the literal bundle assertions stay honest.
---

# Open WebUI Fork Edits

The chat front end is a heavily modified fork and that is settled policy
(`.wolf/decisions.md` D-036, D-040, D-044, D-052). Nothing here is a reason to
refuse fork work. What it is: three separate layers reach the running chat, and
putting a change in the wrong one is silent, not loud.

## Layer 1: the frontend, built from source

`deploy/docker/Dockerfile.open-webui`'s first stage compiles
`vendor/open-webui` the way upstream's own Dockerfile does (node alpine,
`npm ci --force`, `npm run build`) and the final stage does
`rm -rf /app/build` then copies the result in.

**Frontend edits under `vendor/open-webui/src` ship.** Edit them directly.

A version assertion in the final stage compares the vendored tree's
`package.json` version against the pinned backend image's own, and fails the
build when they diverge. That is deliberate: the backend serves whatever is in
`/app/build`, so a frontend built from a different upstream release would call
endpoints that moved.

## Layer 2: the backend, from a pinned upstream image

The Python backend and its whole dependency set come from the pinned upstream
image. Only `/app/build` is replaced.

**A backend edit under `vendor/open-webui/backend` is INERT.** It compiles into
nothing, ships nowhere, and produces no error. This is the single most
expensive thing to rediscover here. Every backend change has to be applied as a
patch script under `deploy/docker/owui-patches/`, invoked by a `RUN python3`
line in the Dockerfile.

The patch scripts come in two shapes:

- **A new module**, copied straight into the image
  (`COPY ... /app/backend/open_webui/utils/hive_*.py`), plus an
  `apply_*_patch.py` that splices the one line mounting it.
- **A rewrite of an upstream file**, an `apply_*_patch.py` that locates an
  anchor and edits around it.

Both are idempotent (a marker check returns early if already applied) and both
**assert their own effect and fail the build loudly**: an anchor that does not
match exactly once prints what it expected and exits 1, and Python splices are
`ast.parse`d before writing so a broken edit never reaches the image. Follow
that pattern in any new patch. A patch that silently no-ops when its anchor
moves is the failure mode the assertions exist to prevent, and it surfaces at
deploy rather than at build.

## Layer 3: `deploy/docker/Caddyfile.owui`

The proxy in front of Open WebUI is a third layer and it can 404 a route
independently of both others. It carries a defence-in-depth `path_regexp` block
that answers 404 on the admin and signup families, on `configs/export`,
`openai/config`, `configs/import` and `configs/namespace`, matched as whole
path segments across casing, trailing slashes, query strings and the
`/api/v1/...` mounts.

Consequence: a route can exist in the frontend, be mounted correctly in the
backend, and still 404 in a browser because Caddy blocks it. When a new route
returns 404 and both other layers look right, read the Caddyfile before
debugging either.

## Keeping the literal bundle assertions honest

`owui-patches/hive_ui_surfaces.py` rewrites verbatim substrings of a compiled
bundle that only exists inside the image, and PR CI never builds that image
(`ci.yml` runs `make test-scripts`; only `owui-nightly.yml` and
`deploy-demo-box.yml` build `Dockerfile.open-webui`). Without a checked-in
sample of real bundle bytes, a `find` string edited into something that matches
nothing passes every test in the repo and fails only at deploy.

`owui-patches/dump_bundle_excerpts.py` closes that. It extracts the pinned
image's `_app/immutable`, writes verbatim excerpts of every rewrite and guard
site into `owui-patches/pinned-bundle-excerpts.json`, and refuses to write
unless each one matches exactly one site, so a stale or wrong bundle cannot
produce a fixture that then certifies itself.
`scripts/test_owui_ui_surfaces.py` checks the rewrite table against that
fixture in PR CI. `pinned-main-excerpts.json` does the same job for
`scripts/test_owui_model_picker_filter.py`.

**Regenerate and commit the fixture after any change to the pinned digest**:

```bash
cid=$(docker create <the digest pinned in Dockerfile.open-webui>)
docker cp "$cid:/app/build/_app/immutable" /tmp/owui-bundle
docker rm -f "$cid"
python3 deploy/docker/owui-patches/dump_bundle_excerpts.py /tmp/owui-bundle
```

## One stale claim to stop repeating

Several patch comments still assert, in their justifying prose, that **every
tenant OWNER holds the Open WebUI `admin` role**. That is no longer true.

`owui-patches/tenant_role_from_db.py` (see the block at lines 17 to 30) revoked
that mapping. OWUI `admin` now requires an ACTIVE `owner` row in
`public.account_memberships` on an account carrying `is_platform_admin = true`,
which is the same predicate the control plane uses for its own platform-admin
surfaces (`apps/control-plane/internal/platform/role_pgx.go`, `IsPlatformAdmin`).
A tenant OWNER resolves to an ordinary OWUI `user`.

The patches themselves remain correct and necessary defence; only the prose
describing the live exposure is stale. Do not cite it as a current finding, and
do not weaken a patch on the grounds that the exposure it guards is already
open.

## Related

- `.wolf/decisions.md` D-036 (the fork is real and heavy; the old patch-only
  ceiling is not a rule), D-044 (Open WebUI is a view; state and knobs belong to
  the control plane; the frontend-only build constraint is stated there too),
  D-052 (upstream tracking of `vendor/open-webui` is revoked, so patch drift is
  no longer a reason to refuse a backend change).
- `.claude/skills/prove-test-load-bearing.md` for confirming a gate's scope
  actually covers the file you changed, which is exactly the trap
  `scripts/test-owui-hive-frontend.sh` set on PR #1298.
