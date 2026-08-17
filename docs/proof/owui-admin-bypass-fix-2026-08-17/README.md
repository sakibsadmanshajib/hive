# Proof: BYPASS_ADMIN_ACCESS_CONTROL / ENABLE_ADMIN_CHAT_ACCESS fix, 2026-08-17

Fixes hive#947. Full audit: `audit-2026-08-17-admin-settings-and-rbac.md` in the
project vault. Plan: `plan-2026-08-17-owui-admin-bypass-fix.md` in the same
vault.

## Method

Ran the fixed `deploy/docker/docker-compose.yml` in an isolated local compose
project (`local` + `chat` profiles, so `open-webui` and `caddy-owui` are both
up), against a fresh Open WebUI volume. Confirmed live inside the running
container that both flags are `false` (`docker exec ... printenv`).

Created two Open WebUI accounts directly against the running container's own
database, using its own internal APIs (`open_webui.models.users.Users`,
`open_webui.models.knowledge.Knowledges`) and minted each a normal Open WebUI
session token with the container's own `create_token`/`WEBUI_SECRET_KEY`, the
same mechanism every real login produces. No password was set, read, reset,
or rotated on any account anywhere in this exercise, and neither account is
the owner's personal account or the shared demo account:

* `tenant-a-owner@hive-verify-947.invalid`, role `admin`, owns one Knowledge
  collection, `Verify-947-Tenant-A-Secret-Kb`.
* `tenant-b-owner@hive-verify-947.invalid`, role `admin`, a different tenant,
  no relationship to the first account or its collection.

Both accounts are `admin` (as every sole tenant OWNER is on this deployment,
audit finding #1), so this isolates exactly the bug in finding #2: whether an
admin session can see a document that belongs to neither itself nor its own
tenant.

## Screenshots

`fixtureA-own-tenant-knowledge.png`: fixture A's own Workspace > Knowledge
page, showing its own collection. Confirms the fix does not hide an admin's
own data (the demo's Knowledge walkthrough only ever touches the signed-in
account's own collection, so it is unaffected).

`fixtureB-cross-tenant-knowledge-absent.png`: fixture B's own Workspace >
Knowledge page. "No knowledge found" -- fixture A's collection is not listed,
even though fixture B is also an Open WebUI admin. Before this fix (upstream
default, `BYPASS_ADMIN_ACCESS_CONTROL` unset and therefore `true`), the same
request would have returned fixture A's collection to fixture B, which is
what the owner observed live on the demo box (a demo account's Knowledge
collection visible from an unrelated personal account).

Neither screenshot contains a URL bar, a cookie, or a token: both are
page-content screenshots (Playwright `page.screenshot()`), not full-window
captures.
