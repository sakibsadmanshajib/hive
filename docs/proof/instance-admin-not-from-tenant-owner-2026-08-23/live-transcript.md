# Issue #748: instance admin no longer comes from a tenant role

Captured 2026-08-23 against a purpose built throwaway stack, not the demo box
and not the shared development database. Nothing live was read or written.

## Why a throwaway stack

The shared `.env` points at a Supabase Cloud project that has been deleted, and
the data plane is now self hosted, so there is no shared stack to run this
against. This capture stands up its own: one Postgres, one Docker network, two
Open WebUI containers, its own ports. Everything is deleted afterwards.

* Postgres: `hive-supabase-db:pg16-cron`, database `hive_748`, password a
  throwaway string on a container with no published port.
* Schema: `.github/ci/test-db-bootstrap.sql` then every file in
  `supabase/migrations/` in order, which is the same recipe
  `.github/workflows/ci.yml` uses for its ephemeral Postgres.
  `20260822_01_metering_retention_pg_cron_schedule.sql` was skipped, because
  pg_cron needs `shared_preload_libraries` and `cron.database_name` set on the
  server and it has nothing to do with the code under test. Every other
  migration applied, including this branch's
  `20260823_03_owui_role_never_admin.sql`.
* Chat containers, an A/B pair built from the exact digest
  `deploy/docker/Dockerfile.open-webui` pins
  (`ghcr.io/open-webui/open-webui:v0.10.2@sha256:9fcea9c6...`), differing only in
  the two files this change touches:
  * `hive-owui-748:before`, carrying `origin/main`'s
    `owui-patches/tenant_role_from_db.py` and `apply_tenant_role_patch.py`.
  * `hive-owui-748:after`, carrying this branch's versions.

  The frontend build stage was skipped for both, deliberately: the decision under
  test is a backend one, and skipping the same stage on both sides keeps the pair
  honest. The backend layers are the ones production ships, applied by the same
  script, and the `after` image's build is itself evidence: the exact literal
  removals asserted their own effect against the real pinned image rather than
  against a lookalike.

## Identities seeded

Five synthetic identities, `.invalid` addresses, generated uuids, no credentials.

```
           email           | platform_operator | tenant_roles
---------------------------+-------------------+--------------
 member@hive-748.invalid   | f                 | MEMBER
 multi@hive-748.invalid    | f                 | MEMBER,OWNER
 operator@hive-748.invalid | t                 |
 owner@hive-748.invalid    | f                 | OWNER
 stranger@hive-748.invalid | f                 |
```

`owner@hive-748.invalid` is the shape issue #748 is about: a legitimately
provisioned business tenant OWNER with exactly one ACTIVE membership, and no
platform attribute. `operator@hive-748.invalid` holds
`accounts.is_platform_admin` plus an ACTIVE `owner` membership and no tenant
membership at all.

## What the running container's own code answers

`OAuthManager.get_user_role`, the exact function the OIDC login path calls,
invoked inside each running container against the real Postgres reachable at
`PGVECTOR_DB_URL`. Nothing stubbed, nothing re-implemented
(driver: `probe-role.py`, reproduced under "Scripts" below).

### Before, with an empty Open WebUI instance

```
open webui accounts in this instance: 0
  operator@hive-748.invalid      -> pending
  owner@hive-748.invalid         -> admin
  member@hive-748.invalid        -> user
  multi@hive-748.invalid         -> pending
  stranger@hive-748.invalid      -> pending
```

The bug, reproduced live: the tenant OWNER is an administrator of the shared
instance, and the actual platform operator is stranded on the activation screen.

### After, same database, same identities

```
open webui accounts in this instance: 0
  operator@hive-748.invalid      -> admin
  owner@hive-748.invalid         -> user
  member@hive-748.invalid        -> user
  multi@hive-748.invalid         -> user
  stranger@hive-748.invalid      -> pending
```

The tenant OWNER is an ordinary user. The platform operator, and only the
platform operator, is admin. The multi membership identity is no longer stranded:
that condition existed to narrow the OWNER promotion, and with the promotion gone
there is nothing left to narrow, so a person who belongs to two tenants can now
use the chat.

### The second promotion path, with exactly one account in the instance

Upstream promotes the only user of the whole instance to admin, and that branch
sits above the Hive lookup, so it decided the role on its own. One native account
was created in each instance to reach `user_count == 1`
(`POST /api/v1/auths/signup`, HTTP 200 on both, generated password never printed).

Before:

```
open webui accounts in this instance: 1
  operator@hive-748.invalid      -> admin
  owner@hive-748.invalid         -> admin
  member@hive-748.invalid        -> admin
  multi@hive-748.invalid         -> admin
  stranger@hive-748.invalid      -> admin
```

Every identity, including one with no Hive membership of any kind, administers
everyone's chat. That is what a fresh volume, a restore or a database reset
produced.

After:

```
open webui accounts in this instance: 1
  operator@hive-748.invalid      -> admin
  owner@hive-748.invalid         -> user
  member@hive-748.invalid        -> user
  multi@hive-748.invalid         -> user
  stranger@hive-748.invalid      -> pending
```

Unchanged. The account count no longer decides anything.

## What the resolved role actually buys

Real requests against the running `after` container, with a bearer minted inside
it for a five minute window on a throwaway secret. The bearer is not reproduced
here: `<redacted, minted in container, 5 minute TTL, throwaway secret>`.

At role `user`, which is what a tenant OWNER now resolves to:

```
role on the account under test: user
  HTTP 200  /api/v1/auths/               customer session
  HTTP 200  /api/v1/models               customer model list
  HTTP 200  /api/v1/chats/list           customer chat list
  HTTP 401  /api/v1/users/               ADMIN user directory
  HTTP 401  /api/v1/configs/export       ADMIN config export
  HTTP 200  /api/v1/functions/           customer functions list (get_verified_user upstream)
  HTTP 401  /api/v1/functions/export     ADMIN functions export (with valves)
```

The same account at role `admin`, which is what it used to resolve to:

```
role on the account under test: admin
  HTTP 200  /api/v1/auths/               customer session
  HTTP 200  /api/v1/models               customer model list
  HTTP 200  /api/v1/chats/list           customer chat list
  HTTP 200  /api/v1/users/               ADMIN user directory
  HTTP 200  /api/v1/configs/export       ADMIN config export
  HTTP 200  /api/v1/functions/           customer functions list (get_verified_user upstream)
  HTTP 200  /api/v1/functions/export     ADMIN functions export (with valves)
```

Two things at once. The role string is the whole difference between reading every
tenant's user directory and being refused, so the demotion is a real loss of
capability rather than a cosmetic label. And every customer surface answers 200
either way, so a legitimately provisioned tenant owner keeps the session, the
model list and their chats. `/api/v1/functions/` is labelled honestly: upstream
gates the list route on `get_verified_user`, not on admin, so its 200 at role
`user` is upstream behaviour and not a hole this change opened. The admin gated
sibling on the same router, `/export`, is refused.

## Browser capture, the real product

A third container, `hive-owui-748:full`, built from the complete
`deploy/docker/Dockerfile.open-webui` on this branch (fork frontend plus every
patch), against the same throwaway Postgres. Signed in as
`owner@hive-748.invalid`, the tenant OWNER, whose Open WebUI role this change
resolves to `user`. The session bearer was minted inside the container and is
not reproduced here or visible in any screenshot.

```
chat url: http://127.0.0.1:3903/
user menu: opened
admin url after navigation: http://127.0.0.1:3903/admin/users
```

No console errors were emitted during the capture.

* `chat-as-tenant-owner.png`: the Hive shell, greeting the account by name,
  composer live, sidebar present. This is the half that has to keep working:
  a legitimately provisioned tenant owner is a customer and loses nothing.
* `user-menu-as-tenant-owner.png`: the account menu carries Settings, Archived
  Chats and Sign Out, and no administrative entry. Stated precisely, because
  the two changes are adjacent: the menu's admin link and the `(app)/admin`
  route tree were removed from the fork by #1091, so their absence here is
  that change's work. What this change removes is the role behind them.
* `admin-attempt-as-tenant-owner.png`: navigating straight to `/admin/users`
  renders `404: Not Found`, again #1091's route removal, captured here as the
  state a demoted account now meets end to end.

The role decision itself is not a UI event, so the evidence for it is the A/B
above and the rendered summary posted on the pull request as
`role-ab-evidence.png`.

## Regression tests, red then green

`apps/control-plane/internal/tenants` against the same throwaway Postgres, with
the pre fix hook definition restored
(`20260727_01_token_hook_membershipless_no_raise.sql` re-applied) and the test
fixtures cleaned first, so the failure is the behaviour and not a leftover row:

```
=== RUN   TestCustomAccessTokenHook_OwuiRoleNeverGrantsInstanceAdmin/OWNER
    Messages: tenant role "OWNER" must not resolve to Open WebUI's admin role:
              instance admin is accounts.is_platform_admin, never a tenant role (issue #748)
=== RUN   TestCustomAccessTokenHook_OwuiRoleNeverGrantsInstanceAdmin/ADMIN
    Messages: tenant role "ADMIN" must not resolve to Open WebUI's admin role: ...
--- FAIL: TestCustomAccessTokenHook_OwuiRoleNeverGrantsInstanceAdmin (0.76s)
    --- FAIL: .../OWNER (0.52s)
    --- FAIL: .../ADMIN (0.06s)
    --- PASS: .../MEMBER (0.08s)
    --- PASS: .../VIEWER (0.06s)
```

With this branch's migration applied, the same command, plus the signup package's
database backed suites:

```
ok  github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenants  0.254s
ok  github.com/sakibsadmanshajib/hive/apps/control-plane/internal/signup   4.260s
```

`scripts/test_owui_tenant_role.py`, the new self check wired into
`make test-scripts`, run against `origin/main`'s two patch files: 10 of its 20
checks fail. Six fail on the behaviour (the platform admin resolution, the
absence of an OWNER comparison, the archived tenant exclusion). Four fail because
`origin/main`'s `apply_tenant_role_patch.py` cannot be pointed at a copy of
`oauth.py` at all, its target path being hardcoded, so those four are honest
failures for an incidental reason and are not claimed as behavioural evidence.
Against this branch, all 20 pass, and `make test-scripts` is green end to end.

## Caveats, stated rather than left to be found

1. The frontend build stage was skipped for both images in the A/B pair, so those
   two containers serve upstream's frontend rather than the Hive fork's. The
   change under test is a backend role decision and the backend layers are
   production's. The browser capture below is against a third container built
   from the full `deploy/docker/Dockerfile.open-webui`, fork frontend included,
   so the product screenshots are the real product.
2. Native signup carries its own first user promotion, in
   `open_webui/routers/auths.py`, which this change does not touch: both images
   answered HTTP 200 with role `admin` to the first `POST /api/v1/auths/signup`.
   On every deployment this repository ships that route is unreachable
   (`ENABLE_SIGNUP` is false in `deploy/docker/docker-compose.yml` and
   `deploy/docker/Caddyfile.owui` answers 404 on the auths routes), and it is a
   password flow for which no password is ever issued to a customer. It is named
   here as a residual and recommended as a follow up rather than folded in, since
   the Caddy surface it depends on is being changed concurrently for #736.
3. The demo box was not touched. What is true of it, and the remediation, is in
   the pull request body.

## Scripts

`probe-role.py`, in full, since the whole claim rests on what it asks:

```python
import asyncio

from open_webui.models.users import Users
from open_webui.utils.oauth import OAuthManager

class ExistingUser:
    def __init__(self, email):
        self.email = email
        self.id = "probe"
        self.role = "pending"

async def main():
    manager = OAuthManager.__new__(OAuthManager)
    count = await Users.get_num_users()
    print(f"open webui accounts in this instance: {count}")
    for email in EMAILS:
        role = await OAuthManager.get_user_role(
            manager, ExistingUser(email), {"email": email}
        )
        print(f"  {email:30s} -> {role}")

asyncio.run(main())
```
