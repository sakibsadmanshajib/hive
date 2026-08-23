# Proof: closing the public chat origin's admin surface

Fixes hive#736, #948 and #949. This directory carries the capture's text logs
only, per `.claude/rules/orchestrator.md` rule 8: the screenshots are posted as
permanent GitHub Release assets through `scripts/post-pr-visual-proof.sh`, not
committed here, because a `raw.githubusercontent.com` link pinned to this
branch's name would 404 the moment the branch is deleted at squash-merge.
`npm run lint:proof-tokens` scans this directory and nothing else, which is why
the logs live here rather than only in a PR comment.

No real credential exists anywhere in this proof. That matters more than usual
here: two of the routes under test return the live gateway key and
`oauth.client_secret` on a real deployment, so everything below runs against an
isolated local stack whose corresponding values are literal placeholders, and
no response body is printed at any point. The probe reports status codes, and
for the disclosure routes it reports which secret-bearing FIELD NAMES a body
carried, never a value.

## 1. The matchers, before and after, against the shipped Caddyfile

`caddy-ab.sh` runs `caddy:2-alpine` with `deploy/docker/Caddyfile.owui`
bind-mounted byte for byte from the repo. The file is not edited to make it
testable: the upstream is replaced by giving one stub container every service
name the Caddyfile proxies to (`open-webui`, `agent-console`, `edge-api`) as a
docker network alias, so every `reverse_proxy` in the shipped file resolves to a
200 responder. What is measured is therefore the shipped matcher set.

`before` is `origin/main` at `e77fbf49e`. `after` is this branch. Full output in
`caddy-before.log` and `caddy-after.log`; the two files differ only in the rows
below.

| request | before | after |
| --- | --- | --- |
| `GET /api/v1/configs/import` | 200 | 404 |
| `GET /api/v1/configs/namespace/oauth` | 200 | 404 |
| `GET /api/v1/configs/namespace/rag` | 200 | 404 |
| `POST /api/v1/functions/create` | 200 | 404 |
| `POST /api/v1/functions/load/url` | 200 | 404 |
| `POST /api/v1/functions/id/<id>/update` | 200 | 404 |
| `POST /api/v1/functions/id/<id>/toggle` | 200 | 404 |
| `POST /api/v1/functions/id/<id>/toggle/global` | 200 | 404 |
| `DELETE /api/v1/functions/id/<id>/delete` | 200 | 404 |
| `POST /api/v1/configs/import` | 200 | 404 |
| `POST /api/v1/configs/code_execution` | 200 | 404 |
| `POST /api/v1/configs/connections` | 200 | 404 |

Every other row is identical in both files, which is the half that matters as
much: 18 product paths answer 200 before and after, including the four that a
subtree-shaped edit would have broken.

| request that must keep working | before | after |
| --- | --- | --- |
| `POST /api/v1/users/user/settings/update` | 200 | 200 |
| `POST /api/v1/users/user/info/update` | 200 | 200 |
| `POST /api/v1/models/model/toggle` | 200 | 200 |
| `POST /api/v1/functions/id/<id>/valves/user/update` | 200 | 200 |
| `POST /api/v1/users/<uuid>/update` | 200 | 200 |

The last row is deliberate and is not a defect this change closes: it is
`get_admin_user`, it is how an administrator mints another administrator, and it
is #437's residue. It stays reachable because `users.py` and `models.py` carry
`get_verified_user` writes at neighbouring paths, so the fix there is a
per-route allowlist rather than a subtree block, and because the nightly Open
WebUI e2e depends on that exact route for its own password sync.

## 2. The same routes against a running Open WebUI, holding a real admin session

`live-proof.sh` brings up `open-webui` and `caddy-owui` from the repo's own
`docker-compose.yml` in an isolated compose project, on an image built from this
branch, and probes two addresses with the same bearer token:

* `http://localhost:3079`, this branch's `caddy-owui` (3003 was already held by
  another agent's stack on this machine);
* `http://127.0.0.1:8080` from inside the `open-webui` container.

The pairing is the whole point. Every route under test is `get_admin_user`, so
an unauthenticated 404 would prove nothing: the session is minted with the
deploy's own `scripts/owui-mint-admin-token.py` for a throwaway admin account
created inside that stack, and Open WebUI itself reports the role back. A 404
from the origin beside a 200 from inside the container is the proxy refusing a
request Open WebUI would have served.

Output in `live-proof.log`.

## 3. The install still works, through its new route

`#736` could not be closed until the deploy stopped depending on the gap. The
same run executes `scripts/install-owui-jwt-forward-in-container.sh` and reads
the end state back out of the Functions API: present, active and global. That
is in `live-proof.log` too, under section 4.

## 4. Admin Settings is not reachable from the Settings dialog

`capture-settings.mjs` drives a browser against two images built from the same
pinned upstream digest and the same `Dockerfile.open-webui`, differing only in
the files this pull request touches, run with `WEBUI_AUTH=false` so the session
is an administrator (upstream promotes the single auto-created account), which
is the condition that made the link render in the first place. It counts the
link out of the DOM as well as photographing the dialog, and then asks the SPA
to navigate to `/admin/settings` exactly as the removed link's `goto()` did, so
the claim covers the route and not only the link.

Logs in `capture-before.log` and `capture-after.log`. Screenshots posted to the
PR as release assets.

## 5. The build now checks its own output

`Dockerfile.open-webui` asserts the built bundle carries no
`/admin/settings/code-execution`, a route string that only reaches the output
through an import chain starting at the deleted route tree. Verified in both
directions rather than assumed:

* an independently built fork image from `main` (`hive-open-webui:v0.10.2-branded`,
  which does carry `data-hive-nav`, so it is a fork build and not upstream's
  bundle) contains the string in 2 chunks;
* the image built from this branch does not contain it, which is what the new
  assertion checks at build time.

## Note on the images

`main`'s chat frontend build is red as of `e77fbf49e`: `<svelte:window>` sits
inside an `{#if}` block in `AgentSchedules.svelte`, which the Svelte compiler
refuses, and `deploy-demo-box` has failed on it since `6884ddfcb`. PR #1085 owns
that fix and this branch deliberately does not carry it. Both proof images were
therefore built with that one-line fix applied to the build tree only, and
nothing else: without it neither image can be produced at all. Every other byte
in the before image is `origin/main` at `e77fbf49e`, and in the after image is
this branch.
