# Issue #1646, tenant_users role reconciliation

Captured 2026-09-01 against the live demo box (`console-hive.scubed.co`) and
against a throwaway Postgres carrying the full migration chain. No credential
appears in any URL below, so nothing here is redacted; the two fixture
addresses are synthetic accounts on a domain that receives no mail.

Sections 6 to 8 were added after this pull request's security review, which
found that the divergence was still being manufactured on current code.

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

**Whose rows these are.** All three promoted rows are automation fixtures
(`invitee2-534` and `invitee534` on the invitation proof tenant,
`rag-verify-e2e` on the RAG verification tenant). No customer row is corrected
by this run. The account #1646 was measured on is on a personal tenant and is
correctly skipped, see section 5.

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

## 4. The deploy time detector, exercised in every direction

`scripts/check-tenant-role-divergence.sh` against the same throwaway Postgres,
one seeded row per case. Cases 5, 6 and 9 are the review's HIGH and MEDIUM
findings: a class nothing can repair is reported, never failed.

```
=== case 1: a consistent pair (expect exit 0) ===                                         exit=0
=== case 2: stale denial, business owner stuck on MEMBER (expect 1) ===                   exit=1
=== case 3: stale grant, tenant OWNER who is not an account owner (expect 1) ===          exit=1
=== case 4: personal tenant on MEMBER, by design (expect 0) ===                           exit=0
=== case 5: ADMIN tier against an account owner, reported not failed (expect 0) ===       exit=0
=== case 6: stale denial on an ARCHIVED tenant, reported not failed (expect 0) ===        exit=0
=== case 7: no mapped pairs at all, zero examined (expect 1) ===                          exit=1
=== case 9: tenant OWNER with NO account membership at all, reported (expect 0) ===       exit=0
=== case 10: tenant OWNER whose ACTIVE membership says member (expect 1) ===              exit=1
=== case 8: wrong cluster identifier (expect 1) ===                                       exit=1
```

Full summary line, from case 1:

```
workspace role divergence on cluster 7680652870670516262: examined 1 mapped pair(s); stale grants 0; stale denials 0; tenant OWNERs with no account membership at all 0 (reported); ADMIN or VIEWER divergences 0 (reported); personal tenants holding MEMBER by design 0; divergences on archived tenants 0 (reported); ACTIVE tenant_users rows on unmapped tenants 0 (outside this check entirely)
Workspace administrator roles agree across public.tenant_users and public.account_memberships, over 1 examined pair(s).
```

Case 7's message is the one the review asked for: zero examined pairs is now a
failure, because "0 of 0" and "0 of 68" printed the same line before.

## 5. What this does not fix

The account the issue was measured on, `qa-tester@hive.test` (signed in
2026-09-01 14:24:36Z, ten minutes before #1646 was filed), is on a PERSONAL
tenant, so it is one of the seven rows section 2 skips. Its `MEMBER` is the
deliberate hardcode in `signup/personal_tenant.go`, not drift, and promoting it
would hand workspace admin authority to every self serve signup. The console
still admits the page shell for such an account and still tells its sole owner
to ask their administrator; that is a separate defect with its own decision to
make, filed as issue #1660.

## 6. The writer that was still manufacturing the defect (review finding)

`accounts.Service.AcceptInvitation` writes `public.account_memberships` through
`ActivateMembership` or `CreateMembership` and never propagated onto
`public.tenant_users`. Owner invitations are a shipped feature (20260727_02),
so "invite a co-owner, they accept" produced the stale denial class on current
code, which is why two of the three rows in section 2 are `invitee*`.

`accounts.Service.syncTenantRole` is now the one helper both writers call.
Red, with the new call removed from `AcceptInvitation`:

```
--- FAIL: TestAcceptInvitation_OwnerSeatReachesTenantUsers (0.15s)
    service_invitation_tenant_role_test.go:175: tenant_users.role = "MEMBER" after an accepted owner invitation, want OWNER (issue #1646: the acceptance path still manufactures the divergence)
--- FAIL: TestAcceptInvitation_ActivatedInvitedSeatReachesTenantUsers (0.18s)
    service_invitation_tenant_role_test.go:209: tenant_users.role = "MEMBER" after activating an invited owner seat, want OWNER
FAIL
```

`TestAcceptInvitation_MemberSeatDoesNotBecomeTenantOwner` passed against that
same neutered build, which is what makes it a control rather than a duplicate.

Green, with the call in place, alongside the pre-existing sync suite:

```
--- PASS: TestAcceptInvitation_OwnerSeatReachesTenantUsers (0.12s)
--- PASS: TestAcceptInvitation_ActivatedInvitedSeatReachesTenantUsers (0.21s)
--- PASS: TestAcceptInvitation_MemberSeatDoesNotBecomeTenantOwner (0.95s)
--- PASS: TestUpdateMemberRole_DemotionRevokesTenantUsersOwnerRole (0.70s)
--- PASS: TestUpdateMemberRole_PromotionGrantsTenantUsersOwnerRole (0.97s)
--- PASS: TestUpdateMemberRole_PersonalTenantNeverPromotedToTenantOwner (0.88s)
--- PASS: TestUpdateMemberRole_PreservesNonOwnerTenantRoleTiers (1.27s)
--- PASS: TestUpdateMemberRole_SameRoleReissueRepairsTenantUsersDrift (0.57s)
--- PASS: TestUpdateMemberRole_PromotionSkipsNonActiveTenantUsersRow (0.54s)
--- PASS: TestSyncTenantMembershipRole_HiveAppPosture (0.66s)
ok  	github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts	7.473s
```

## 7. The gate's own numbers against the live box, before and after

The review asked that the fixture corpus be audited before the gate is armed,
rather than each row being found by a red deploy. The check's exact query was
run against the demo box, then the migration was applied inside a transaction
and the query re-run, then rolled back.

```
== A. BEFORE the migration
 examined | stale_grants | orphan_owners | stale_denials | tier_diverged | personal_by_design | archived_diverged | unmapped_active
----------+--------------+---------------+---------------+---------------+--------------------+-------------------+-----------------
       68 |            0 |             2 |             3 |             0 |                  7 |                 0 |              49

== D. AFTER the migration, still inside the uncommitted transaction
 examined | stale_grants | orphan_owners | stale_denials | tier_diverged | personal_by_design | archived_diverged | unmapped_active
----------+--------------+---------------+---------------+---------------+--------------------+-------------------+-----------------
       68 |            0 |             2 |             0 |             0 |                  7 |                 0 |              49

ROLLBACK
== E. rolled back, live data untouched
 live_stale_denials_still_present
----------------------------------
                                3
```

So the gate fails on the box today and is satisfied by this pull request's own
migration, which is the property that makes it legitimate to arm.

**The audit changed the script.** The first version of this check failed on any
`tenant_users` OWNER without an owner membership, and that class is not empty
on the box: two OWUI end to end identities (`owui-e2e+c***`, `owui-e2e+p***`)
hold OWNER on tenant `owui-e2e` with zero `account_memberships` rows at all,
because `scripts/seed-owui-e2e-user.py` writes `tenant_users` directly and
never writes a membership. `syncTenantRole` cannot repair those (its join finds
no membership and reports `no_match`), so the check would have blocked every
deploy from the moment it was armed. They are now their own reported class,
`orphan_owners`, and the failing grant class is narrowed to rows where an
ACTIVE membership exists and disagrees, which is the repairable #1245 shape.

## 8. The fixture corpus, as it stands on the box

Every ACTIVE `tenant_users` row on a mapped tenant, grouped by how it pairs
with the account membership. This is the audit the review asked for; the
writers behind each shape are `seed-demo-owner.py`, `seed-owui-e2e-user.py`,
`e2e-fixture-seed.mjs` and `verify-rag-roundtrip.py`.

```
                  tenant_slug                  | personal | archived | tu_role |     am_role     | rows
-----------------------------------------------+----------+----------+---------+-----------------+------
 e2e-verified-tenant-e21c3d7d                  | f        | f        | OWNER   | owner           |    1
 e2e-verified-tenant-f365968c                  | f        | f        | OWNER   | owner           |    1
 e2e-vfy-1787485021                            | f        | f        | OWNER   | owner           |    1
 hive-demo                                     | f        | f        | MEMBER  | (no membership) |    1
 hive-demo                                     | f        | f        | OWNER   | owner           |    2
 hive-owner                                    | f        | f        | OWNER   | owner           |    1
 hive-owner-demo                               | f        | f        | OWNER   | owner           |    1
 hive-uiux-proof                               | f        | f        | OWNER   | owner           |    1
 hive-verify-947                               | f        | f        | MEMBER  | (no membership) |    2
 hive-verify-947-b                             | f        | f        | MEMBER  | (no membership) |    2
 invite534-proof                               | f        | f        | MEMBER  | member          |    2
 invite534-proof                               | f        | f        | MEMBER  | owner           |    2
 invite534-proof                               | f        | f        | OWNER   | owner           |    1
 owui-782-proof                                | f        | f        | MEMBER  | (no membership) |    1
 owui-e2e                                      | f        | f        | MEMBER  | (no membership) |   30
 owui-e2e                                      | f        | f        | OWNER   | (no membership) |    2
 personal-*                                    | t        | f        | MEMBER  | owner           |    7
 personal-9f3866c9-3874-4574-afda-1e339246e821 | t        | f        | OWNER   | owner           |    1
 rag-verify-e2e                                | f        | f        | MEMBER  | owner           |    1
 showcase                                      | f        | f        | MEMBER  | (no membership) |    1
```

Only two shapes are in a failing class: `MEMBER | owner` on a business tenant
(the three rows this migration clears) and, hypothetically, an OWNER whose
ACTIVE membership disagrees, of which there are none. Everything reading
`(no membership)` is outside both failing classes by construction, which is
what the `orphan_owners` split above encodes.
