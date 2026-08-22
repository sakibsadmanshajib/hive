# Proof: BYPASS_ADMIN_ACCESS_CONTROL / ENABLE_ADMIN_CHAT_ACCESS fix

Fixes hive#947, PR #960. This directory carries the capture's text log only,
per `.claude/rules/orchestrator.md` rule 8: the images themselves are posted
as permanent GitHub Release assets (`scripts/post-pr-visual-proof.sh`), not
committed here, because a `raw.githubusercontent.com` link pinned to this
branch's name would 404 the instant the branch is deleted at squash-merge.
`npm run lint:proof-tokens` scans this directory for credential-shaped text,
which is the reason the text log lives here rather than only in a PR comment.

## Method

Ran the fixed `deploy/docker/docker-compose.yml` in an isolated local compose
project, `local` + `chat` profiles, against a fresh Open WebUI volume.
Created two Open WebUI accounts directly through the running container's own
internal APIs (`open_webui.models.users.Users`,
`open_webui.models.knowledge.Knowledges`), each minted a normal Open WebUI
session token with the container's own `create_token`/`WEBUI_SECRET_KEY` (the
same primitive every real login produces). No password was set, read, reset,
or rotated on any account, and neither account is the owner's personal
account or the shared demo account.

**Control, addressing review feedback that the first round of screenshots
could not distinguish "the fix works" from "the test account was never
admin":** same two accounts, same Knowledge collection, captured twice
against the same container/volume, with only the two flags flipped between
captures, and each account's role independently confirmed via
`GET /api/v1/auths/` at the moment of each capture (not asserted in prose).

- `tenant-a-owner@hive-verify-947.invalid` (admin, tenant A): owns one
  Knowledge collection, `Verify-947-Tenant-A-Secret-Kb`.
- `tenant-b-owner@hive-verify-947.invalid` (admin, tenant B): no relationship
  to fixture A or its collection.

Role check JSON, captured via the same bearer tokens used for each
screenshot:

```
before: {"roleA":{"role":"admin","email":"tenant-a-owner@hive-verify-947.invalid"},"roleB":{"role":"admin","email":"tenant-b-owner@hive-verify-947.invalid"},"bSeesFixtureACollection":true}
after:  {"roleA":{"role":"admin","email":"tenant-a-owner@hive-verify-947.invalid"},"roleB":{"role":"admin","email":"tenant-b-owner@hive-verify-947.invalid"},"bSeesFixtureACollection":false}
```

- **BEFORE** (`BYPASS_ADMIN_ACCESS_CONTROL`/`ENABLE_ADMIN_CHAT_ACCESS` forced
  `true`, simulating the upstream default this PR closes): fixture B, an
  admin in a different tenant, sees fixture A's Knowledge collection.
- **AFTER** (this branch's actual `docker-compose.yml`, both `false`; Open
  WebUI container recreated, same DB volume, same accounts, same
  collection): fixture B sees "No knowledge found".

## Images

Posted as release assets, linked from the PR comment (not hotlinked here, so
this file carries no branch-pinned image reference):

- https://github.com/sakibsadmanshajib/hive/pull/960#issuecomment-5325135646

Local URL: `http://localhost:3003` (an isolated local compose project, never
a public address). No credential appears in any captured URL for this flow
(session tokens were injected via `localStorage`, not a URL query string or
fragment), so no redaction was needed in either the text or the pixels.
