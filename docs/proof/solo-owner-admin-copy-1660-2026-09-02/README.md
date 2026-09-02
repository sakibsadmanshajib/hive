# Issue #1660: a personal tenant's sole owner is told to ask their administrator

Captured 2026-09-02 against a stack running on one machine, twice, with the
same harness and the same two accounts: once from images built at `origin/main`
(`78e8d6d73`), once from images built from this pull request's branch. The two
runs differ in nothing but the two images, so the lines below are comparable
row for row.

No credential appears in any URL here, so nothing is redacted. Both fixture
addresses are on `.invalid`, a domain reserved by RFC 2606 that resolves
nowhere and receives no mail, and both passwords were generated per run inside
a throwaway GoTrue that was destroyed with the capture. No shared test account
was touched and no password was set, reset or rotated anywhere.

Screenshots are attached to the pull request through
`scripts/post-pr-visual-proof.sh` (a permanent release asset, not a branch
URL). The logs are here because `npm run lint:proof-tokens` scans this
directory and nothing else.

## What was running

| Piece | Detail |
| --- | --- |
| Database | Throwaway Postgres (`pgvector/pgvector:pg17`), full `supabase/migrations` chain applied by `scripts/apply-migrations.sh` |
| Auth and REST | GoTrue plus PostgREST behind one gateway, stood up by a renamed copy of `scripts/ci-supabase-stack.sh` (renamed only so it could not collide with another agent's containers on the same host) |
| control-plane | `hive-control-plane:ci`, built from the tree under test |
| Console | `hive-web-console-prod:ci`, built from the same tree, `next start` behind the compose service |
| Browser | Playwright Chromium, 1440x900, real sign-in through `/auth/sign-in` |

## The two accounts

Both are ACTIVE `owner` rows in `public.account_memberships`, provisioned by
the control-plane itself on first `GET /api/v1/viewer`
(`accounts.Service.provisionDefaultWorkspace`). They differ in one column, the
one `platform.WorkspaceAdminGate` authorizes on.

| account | tenant | `tenant_users.role` | seeded the way production writes it |
| --- | --- | --- | --- |
| `solo-owner-...@hiveproof.invalid` | personal (`personal_owner_user_id` set) | `MEMBER` | `signup.ensurePersonalTenant` plus `signup.insertPersonalMembership` |
| `tenant-owner-...@hiveproof.invalid` | business | `OWNER` | the control, an ordinary tenant owner |

## Before, on `origin/main`

The defect reproduces exactly as reported. The personal tenant's sole owner is
offered both admin nav entries, both pages return 200, and both render the
403 empty state telling them to ask an administrator who does not exist.

```
{"run":"before-main","account":"personal-tenant-sole-owner","tenant_users_role":"MEMBER","url":"/console","http":200,"admin_nav_links":2}
{"run":"before-main","account":"personal-tenant-sole-owner","tenant_users_role":"MEMBER","url":"/console/marketplace","http":200,"wall_marketplace":true,"row_label_old":true}
{"run":"before-main","account":"personal-tenant-sole-owner","tenant_users_role":"MEMBER","url":"/console/feature-gates","http":200,"wall_feature_gates":true,"row_label_old":true}
{"run":"before-main","account":"business-tenant-owner","tenant_users_role":"OWNER","url":"/console/marketplace","http":200,"marketplace_empty_old":true}
{"run":"before-main","account":"business-tenant-owner","tenant_users_role":"OWNER","url":"/console/feature-gates","http":200,"row_label_old":true}
```

The control's two lines are the second half of the same defect, and they are
why this pull request does not stop at the 403 wall: a genuine tenant owner,
alone in their workspace, was told on the marketplace page to "ask your
administrator if you need one added" and on every platform-only feature-gate
row that it is "Managed by your administrator". Both name somebody who is not
there.

Full log: `capture-before-main.jsonl`.

## After, on this branch

```
{"run":"after-branch","account":"personal-tenant-sole-owner","tenant_users_role":"MEMBER","url":"/console","http":200,"admin_nav_links":0}
{"run":"after-branch","account":"personal-tenant-sole-owner","tenant_users_role":"MEMBER","url":"/console/marketplace","http":404,"headings":["Page not found"],"wall_marketplace":false}
{"run":"after-branch","account":"personal-tenant-sole-owner","tenant_users_role":"MEMBER","url":"/console/feature-gates","http":404,"headings":["Page not found"],"wall_feature_gates":false}
{"run":"after-branch","account":"business-tenant-owner","tenant_users_role":"OWNER","url":"/console/marketplace","http":200,"marketplace_empty_old":false}
{"run":"after-branch","account":"business-tenant-owner","tenant_users_role":"OWNER","url":"/console/feature-gates","http":200,"row_label_old":false,"row_label_new":true}
```

Three things are proven together, which is why the control is in the same run:

1. The personal tenant's sole owner is no longer offered a surface the
   control-plane refuses. No nav entry, 404 on the URL, and the 404 body is the
   real not-found page ("This page does not exist, or it is not available on
   this account", with a way back), not a blank shell.
2. No administrator-referral copy renders for them anywhere. Every probe is
   false.
3. Nothing was taken from a caller who does have the authority. The control
   still reaches both pages, still gets its real gate list with working
   toggles, and its platform-only rows now read "Managed by the platform"
   rather than naming an administrator.

Full log: `capture-after-branch.jsonl`.

## Probe strings

The log's booleans match these exact strings, whitespace normalized:

| key | string |
| --- | --- |
| `wall_marketplace` | Ask your workspace owner or administrator if you need a connector enabled. |
| `wall_feature_gates` | Ask your workspace owner or administrator if you need a capability turned on. |
| `row_label_old` | Managed by your administrator |
| `row_label_new` | Managed by the platform |
| `marketplace_empty_old` | Ask your administrator if you need one added. |

`row_label_old` is deliberately matched on the row label rather than the
`EmptyState` title, which used to be the same string: PR #1658's capture logged
a false positive by matching the shorter one, and this branch removes the
collision by renaming the row label.

## Unit and build runs behind the same change

```
docker compose --profile tools run --rm toolchain "cd /workspace && go test ./apps/control-plane/... -count=1 -short"
docker compose run --rm --no-deps --build web-console npm run test:unit    # 1283 passed
docker compose run --rm --no-deps --build web-console npm run build
```
