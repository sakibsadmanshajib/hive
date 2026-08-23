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

## 1b. Can a normalisation trick get past the new matcher

`traversal-probe.sh`, output in `traversal-after.log`. Every request is sent
with `curl --path-as-is`, so the dot segments and doubled slashes are not
collapsed on the client side the way a browser would collapse them. Seventeen
variants of the blocked paths, all refused:

```
POST //api/v1/functions/create                            404
POST ///api/v1/functions/create                           404
POST /api/v1//functions/create                            404
POST /api/v1/functions//create                            404
POST /API/v1/FUNCTIONS/Create                             404
POST /api/v99/functions/create                            404
POST /api/v1/functions/id/../create                       404
POST /api/v1/hive/../functions/create                     404
POST /api/v1/hive/../configs/import                       404
POST /api//v1/configs/import                              404
GET  /api/v1/configs/namespace/../namespace/oauth         404
GET  /api/v1/./configs/namespace/oauth                    404
POST /api/v1/functions%2Fcreate                           404
POST /api/v1%2Fconfigs%2Fimport                           404
POST /api/v1/functions/id/../valves/user/update           404
POST /api/v1/functions/id/x/valves/user/update            200   <- the carve-out
```

Two things worth recording because they are easy to get wrong from reading
alone. Caddy normalises the request path before matching, which is why
`/api/v1/hive/../configs/import` is refused rather than forwarded as a literal
string that Open WebUI might later resolve. And the `not` arm admits only the
exact user-valves shape: the same path with a `..` for the function id
normalises into the blocked subtree and is refused, so the carve-out cannot be
used as a way in.

## 2. The same routes against a running Open WebUI, holding a real admin session

`live-proof.sh` brings up `open-webui` and `caddy-owui` from the repo's own
`docker-compose.yml` in an isolated compose project, on an image built from this
branch, and probes two addresses with the same bearer token:

* `http://localhost:3079`, this branch's `caddy-owui` (3003 was already held by
  another agent's stack on this machine);
* `http://127.0.0.1:8080` from inside the `open-webui` container.

The pairing is the whole point. Every route under test is `get_admin_user`, so
an unauthenticated 404 would prove nothing. The session is minted by the
deploy's own `scripts/owui-mint-admin-token.py` for a throwaway admin account
created inside that stack, and Open WebUI reports the role back itself:

```
session role as reported by Open WebUI itself: admin proof-admin@hive-verify-736.invalid
```

Verbatim from `live-proof.log`:

```
POST   /api/v1/functions/create                             origin=404
POST   /api/v1/functions/id/hive_jwt_forward/update         origin=404
POST   /api/v1/functions/id/hive_jwt_forward/toggle         origin=404
POST   /api/v1/functions/id/hive_jwt_forward/toggle/global  origin=404
DELETE /api/v1/functions/id/hive_jwt_forward/delete         origin=404
GET    /api/v1/functions/    inside={"status": 200, "secret_fields_present": []}

GET    /api/v1/configs/namespace/oauth                      origin=404
GET    /api/v1/configs/namespace/rag                        origin=404
POST   /api/v1/configs/import                               origin=404
GET    /api/v1/configs/export                               origin=404
GET    /api/v1/configs/namespace/oauth  inside={"status": 200, "secret_fields_present": ["oauth.client_secret"]}
GET    /api/v1/configs/namespace/rag    inside={"status": 200, "secret_fields_present": ["rag.openai.api_key"]}

GET    /api/config                                          origin=200
GET    /api/v1/configs/banners                              origin=200
GET    /api/v1/models/list                                  origin=200
POST   /api/v1/users/user/settings/update                   origin=200
POST   /api/v1/functions/id/hive_jwt_forward/valves/user/update origin=401
```

The two `inside=` lines for the #948 routes are the finding, stated as field
names because a value must never be printed: the same admin session that gets
404 at the origin gets 200 in the container, and the body carries
`oauth.client_secret` and `rag.openai.api_key`, which is `OWUI_SHIM_KEY`. That
is the disclosure the origin now refuses. On this stack both values are
placeholders.

The `401` on the last line is not a block and is worth naming so nobody reads
it as one. It is Open WebUI answering, which is what proves the request passed
the proxy: the carve-out let it through, and at that point in the run the
function did not exist yet, and v0.10.2 answers an unknown function id with 401
rather than 404 (see `scripts/install-owui-jwt-forward.py`'s `read_function`).
A proxy refusal in this table is always a 404.

## 3. The install still works, through its new route

`#736` could not be closed until the deploy stopped depending on the gap. The
same run executes `scripts/install-owui-jwt-forward-in-container.sh` against
the stack above and reads the end state back out of the Functions API:

```
wrapper exit code: 0
installed function: {"id": "hive_jwt_forward", "name": "Hive JWT Forward",
                     "type": "filter", "is_active": true, "is_global": true}
```

Present, active and global, which is the only end state the installer accepts,
and reached without any request crossing the public origin.

## 4. Admin Settings is not reachable from the Settings dialog

`capture-settings.mjs` drives a browser against two images with no compose file
at all, `WEBUI_AUTH=false` so the single auto-created account is an
administrator, which is the condition that made the link render. It reads the
link count out of the DOM as well as photographing the dialog, clicks the link
where one exists, and then navigates to `/admin/settings` as a fresh document.
The compose-less run is deliberate: what changed is in the bundle, so the image
is the thing under test, and with no proxy in front it is Open WebUI's own
router that decides what `/admin/settings` renders. That separates "the route
is gone from the product" from "a proxy answers 404 for it"; this change makes
both true, and the second was already true before it.

`capture-before.log` (`hive-open-webui:v0.10.2-branded`, a fork build from
`main`):

```
[before] session role, read from Open WebUI itself: admin admin@localhost
[before] settings admin links in the DOM: 1
[before] settings tab rail text: ["Search General Interface Audio Data Controls Account About Admin Settings"]
[before] after clicking the Admin Settings link: {"path":"/admin/settings/general","hasAdminPanel":true,
         "bodyStart":"... Users Analytics Evaluations Functions Settings Search General Authentication Conn"}
[before] direct navigation to /admin/settings: {"path":"/admin/settings/general","hasAdminPanel":true, ...}
[before] 4xx/5xx seen: []
```

`capture-after.log` (this branch):

```
[after] session role, read from Open WebUI itself: admin admin@localhost
[after] settings admin links in the DOM: 0
[after] settings tab rail text: ["Search General Interface Audio Data Controls Account About"]
[after] no Admin Settings link to click
[after] direct navigation to /admin/settings: {"path":"/admin/settings","hasAdminPanel":false,
        "bodyStart":"404: Not Found"}
[after] 4xx/5xx seen: []
```

Same role in both runs, which is the control: the difference is the surface,
not the session. Screenshots posted to the pull request as release assets:
the Settings dialog in each image, the admin panel the before image opens on a
click, and the after image's not-found view at the same address.

## 5. The build now checks its own output

`Dockerfile.open-webui` asserts the built bundle carries no
`/admin/settings/code-execution`, a route string from
`lib/components/admin/Settings.svelte`'s own tab table that reaches the output
only through an import chain starting at the deleted route tree. Counted in
both images rather than asserted once (`bundle-evidence.sh`, output in
`bundle-evidence.log`):

```
before (main)         /admin/settings/code-execution   2 chunk(s)
after (this branch)   /admin/settings/code-execution   0 chunk(s)

control, unchanged:
before (main)         data-hive-nav                    2 chunk(s)
after (this branch)   data-hive-nav                    2 chunk(s)
before (main)         "/workspace/models"              0 chunk(s)
after (this branch)   "/workspace/models"              0 chunk(s)

before (main)         Admin Settings                  64 chunk(s)
after (this branch)   Admin Settings                  63 chunk(s)
```

The `data-hive-nav` control matters: it says both images are our own frontend
build rather than upstream's prebuilt bundle, so the difference in the first
pair is this change and not a different build path.

The last pair is the honest caveat. `Admin Settings` drops by one chunk, the
component's, and survives 63 times because it is a translation key in every
bundled locale file. That is why the assertion uses the route string and not
the label: the label cannot go to zero, so an assertion on it would be a check
that can never pass.

## How the two images were built, and what is not the shipped path

Two deviations, both disclosed because a proof built differently from the
product is worth exactly what it is honest about.

**One: `main`'s chat frontend build is red.** As of `e77fbf49e`,
`<svelte:window>` sits inside an `{#if}` block in `AgentSchedules.svelte`, which
the Svelte compiler refuses, and `deploy-demo-box` has failed on it since
`6884ddfcb`. PR #1085 owns that fix and this branch deliberately does not carry
it, so the frontend for the after image was built with that one-line fix applied
to the build tree only. Nothing else was changed. Without it no image can be
produced from this branch at all.

**Two: the after image is the last two steps of `Dockerfile.open-webui`, not a
full run of it.** `proof-image.Dockerfile` is `FROM
hive-open-webui:v0.10.2-branded`, then `rm -rf /app/build`, then the frontend
built from this branch, then the built-bundle assertions verbatim including the
line this pull request adds. The base is the fully patched fork image, so every
backend patch, every `ENV` default and the pinned upstream digest are the
shipped ones.

The full `docker build` could not complete on this machine. The frontend stage's
`npm run build` runs `prepare-pyodide.js`, which downloads about sixty wheels
from `cdn.jsdelivr.net` on every attempt; three attempts died mid-download on
`SocketError: other side closed` and one on the Alpine package CDN, under this
machine's saturated egress (28 containers, sibling agents building), and a
failed layer discards its wheel cache. The frontend was therefore built by the
same commands in a container with a persistent volume, which lets a retry
resume, and the result was layered on. The one thing this does not exercise is
the version-equality assertion between the vendored `package.json` and the
image's, which is unrelated to anything here and runs on every real build.

The before image is `hive-open-webui:v0.10.2-branded` as built on this machine
from `main` earlier the same day. Its provenance is asserted rather than
trusted: it carries `data-hive-nav`, so it is a fork build, and it renders the
Admin Settings link, which is the defect #949 describes.
