# Issue #1646, tenant_users role promotion backfill

Captured 2026-09-01 against the live demo box (`console-hive.scubed.co`) and
against a throwaway Postgres carrying the full migration chain. No credential
appears in any URL below, so nothing here is redacted; the two fixture
addresses are synthetic accounts on a domain that receives no mail.

## 1. The defect, live, with a control on the same workspace

Both accounts below are ACTIVE `owner` rows in `public.account_memberships`
for the same account (`owner534-s-workspace`), which is the only account mapped
to tenant `invite534-proof`. The console's own page gate
(`isWorkspaceAdminViewer`) therefore admits both. They differ in one column:
`public.tenant_users.role`, which is what `platform.WorkspaceAdminGate`
authorizes on.

| account | account_memberships.role | tenant_users.role |
| --- | --- | --- |
| `invitee2-534@hiveproof.dev` | owner | MEMBER |
| `owner534@hiveproof.dev` | owner | OWNER |

Sessions minted through the admin one-time-token flow (`live-auth.mjs`'s
`sessionCookies`, admin calls tunnelled to the internal Supabase listener
because the public origin refuses `/auth/v1/admin/*` by design). No password
was set, reset or rotated.

```
{"account":"diverged-coowner","url":"https://console-hive.scubed.co/console/marketplace","http":200,"headings":"MCP and skills marketplace","workspace_admin_empty_state":true,"captured_at":"2026-09-01T15:02:29.901Z"}
{"account":"diverged-coowner","url":"https://console-hive.scubed.co/console/feature-gates","http":200,"headings":"Feature gates","workspace_admin_empty_state":true,"captured_at":"2026-09-01T15:02:35.483Z"}
{"account":"tenant-owner","url":"https://console-hive.scubed.co/console/marketplace","http":200,"headings":"MCP and skills marketplace","workspace_admin_empty_state":false,"captured_at":"2026-09-01T15:02:44.659Z"}
{"account":"tenant-owner","url":"https://console-hive.scubed.co/console/feature-gates","http":200,"headings":"Feature gates | ADMIN | SOVEREIGN WORKSPACE | BILLING & PAYMENTS","workspace_admin_empty_state":false,"captured_at":"2026-09-01T15:02:50.622Z"}
```

`workspace_admin_empty_state` matches the empty state's own description text
("Ask your workspace owner or administrator"), not the per-row "Managed by
your administrator" label the feature-gate rows carry for platform-only gates.
The first version of this capture matched the shorter string and reported a
false positive on the control's feature-gates page; that is why the log names
the longer one.

Screenshots are attached to the pull request through
`scripts/post-pr-visual-proof.sh` (release asset, not a branch URL).

## 2. What the migration does to the live rows, without committing anything

The migration file was run inside `BEGIN ... ROLLBACK` against the demo box's
database. Nothing was committed; step 4 re-reads the live count afterwards to
prove that.

```
== 1. before: every ACTIVE tenant_users row whose mapped account membership is an active owner, and whose tenant role is not OWNER
                  tenant_slug                  | personal | tu_role |     who
-----------------------------------------------+----------+---------+--------------
 invite534-proof                               | f        | MEMBER  | invitee2-***
 invite534-proof                               | f        | MEMBER  | invitee53***
 rag-verify-e2e                                | f        | MEMBER  | rag-verif***
 personal-0081d6d7-2f5b-480f-961a-a217ea0dfae2 | t        | MEMBER  | qa-zerocr***
 personal-11111111-1111-1111-1111-111111111111 | t        | MEMBER  | test@test***
 personal-54c0d294-c5a3-4ff4-aba2-e4a229519229 | t        | MEMBER  | browser-s***
 personal-a27fb939-b056-4a2c-ae76-d369e6532822 | t        | MEMBER  | e2e-conse***
 personal-b0b3eccf-b876-4cf9-8eb9-77102ae50627 | t        | MEMBER  | qa-tester***
 personal-e5523551-82d7-44e0-ab53-70ccd6757b8a | t        | MEMBER  | uat.test.***
 personal-f4c11051-a1aa-4c4e-9d56-320ca2526bd6 | t        | MEMBER  | phase5-ua***
(10 rows)

== 2. the migration, inside a transaction that is rolled back below
BEGIN
DO
NOTICE:  tenant_users role promotion backfill (issue #1646): promoted 3 row(s) to OWNER on business tenants; skipped 7 personal tenant row(s) by design (see this file's header); left 0 row(s) whose tenant_users role is a tier this migration will not widen (ADMIN or VIEWER) -- scripts/check-tenant-role-divergence.sh reports those on every deploy

== 3. after, still inside the uncommitted transaction: same population, current roles
                  tenant_slug                  | personal | tu_role |     who
-----------------------------------------------+----------+---------+--------------
 invite534-proof                               | f        | OWNER   | invitee2-***
 invite534-proof                               | f        | OWNER   | invitee53***
 invite534-proof                               | f        | OWNER   | owner534@***
 rag-verify-e2e                                | f        | OWNER   | rag-verif***
 personal-0081d6d7-2f5b-480f-961a-a217ea0dfae2 | t        | MEMBER  | qa-zerocr***
 personal-11111111-1111-1111-1111-111111111111 | t        | MEMBER  | test@test***
 personal-54c0d294-c5a3-4ff4-aba2-e4a229519229 | t        | MEMBER  | browser-s***
 personal-9f3866c9-3874-4574-afda-1e339246e821 | t        | OWNER   | post-depl***
 personal-a27fb939-b056-4a2c-ae76-d369e6532822 | t        | MEMBER  | e2e-conse***
 personal-b0b3eccf-b876-4cf9-8eb9-77102ae50627 | t        | MEMBER  | qa-tester***
 personal-e5523551-82d7-44e0-ab53-70ccd6757b8a | t        | MEMBER  | uat.test.***
 personal-f4c11051-a1aa-4c4e-9d56-320ca2526bd6 | t        | MEMBER  | phase5-ua***
(12 rows)

ROLLBACK
== 4. rolled back: the live database is untouched by this dry run
 business_rows_still_diverged_live
-----------------------------------
                                 3
```

Two rows in step 3 that were already OWNER before the run are shown because
step 3 lists the whole population rather than only the changed rows:
`owner534@***` (the control account above) and `post-depl***`, a personal
tenant that already carried OWNER before this work started. That second one is
a pre-existing anomaly this migration deliberately does not touch in either
direction: it grants nothing new, and revoking it would change what
`post-deploy-verify` can reach without anyone having decided that.

## 3. The regression suite, red then green

`apps/control-plane/internal/accounts/migration_tenant_role_backfill_test.go`
runs the migration file itself against a throwaway Postgres built from the full
chain (`pgvector/pgvector:pg17`, `.github/ci/test-db-bootstrap.sql`, then every
file in `supabase/migrations/` in order).

Red, with the migration body replaced by a no-op:

```
--- FAIL: TestPromoteBackfill_BusinessTenantAccountOwnerIsPromoted (0.14s)
    migration_tenant_role_backfill_test.go:90: tenant_users.role = "MEMBER" after the backfill, want OWNER (issue #1646: a real workspace owner is still 403'd on the feature gate and marketplace surfaces)
FAIL
```

The other four cases passed against that same no-op, which is the point of
them: they assert what the migration must NOT change.

Green, with the real migration:

```
--- PASS: TestPromoteBackfill_BusinessTenantAccountOwnerIsPromoted (0.21s)
--- PASS: TestPromoteBackfill_PersonalTenantIsNeverPromoted (0.18s)
--- PASS: TestPromoteBackfill_AccountMemberIsNeverPromoted (0.24s)
--- PASS: TestPromoteBackfill_SuspendedTenantRowIsNeverPromoted (0.24s)
--- PASS: TestPromoteBackfill_AdminTierIsNotWidened (0.23s)
ok  	github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts	1.106s
```

## 4. The deploy time detector, exercised in both directions

`scripts/check-tenant-role-divergence.sh` against the same throwaway Postgres,
one seeded row per case.

```
=== case 1: no divergence (expect exit 0) ===
workspace role divergence on cluster 7680579066794696742: stale grants 0, stale denials 0, personal tenants holding MEMBER by design 0
Workspace administrator roles agree across public.tenant_users and public.account_memberships.
exit=0

=== case 2: stale denial, business tenant owner stuck on MEMBER (expect exit 1) ===
workspace role divergence on cluster 7680579066794696742: stale grants 0, stale denials 1, personal tenants holding MEMBER by design 0
::error::1 public.tenant_users row(s) on a business tenant deny a user public.account_memberships says is an active owner of the mapped account. [...]
exit=1

=== case 3: stale grant, tenant OWNER whose membership is only a member (expect exit 1) ===
workspace role divergence on cluster 7680579066794696742: stale grants 1, stale denials 0, personal tenants holding MEMBER by design 0
::error::1 public.tenant_users row(s) still carry role OWNER for a user public.account_memberships no longer considers an active owner of the mapped account. [...]
exit=1

=== case 4: personal tenant on MEMBER, by design (expect exit 0, counted) ===
workspace role divergence on cluster 7680579066794696742: stale grants 0, stale denials 0, personal tenants holding MEMBER by design 1
Workspace administrator roles agree across public.tenant_users and public.account_memberships.
exit=0

=== case 5: wrong cluster identifier (expect exit 1) ===
::error::workspace role check is connected to cluster 7680579066794696742 but the stack's database is cluster 0. Refusing to report on a database the application does not use.
exit=1
```

## 5. What this does not fix

The account the issue was measured on, `qa-tester@hive.test` (signed in
2026-09-01 14:24:36Z, ten minutes before #1646 was filed), is on a PERSONAL
tenant, so it is one of the seven rows step 2 skips. Its `MEMBER` is the
deliberate hardcode in `signup/personal_tenant.go`, not drift, and promoting it
would hand workspace admin authority to every self serve signup. The console
still admits the page shell for such an account and still tells its sole owner
to ask their administrator; that is a separate defect with its own decision to
make, filed as issue #1660.
