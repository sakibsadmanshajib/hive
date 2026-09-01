#!/usr/bin/env bash
# check-tenant-role-divergence.sh -- Fail when public.tenant_users.role and
# public.account_memberships.role disagree about who administers a workspace,
# on the database this stack uses.
#
# Why this exists (issues #1244, #1245, #1646)
# -------------------------------------------
# Two tables answer "is this person the administrator of this workspace", and
# only one of them is written by a live product flow. public.account_memberships
# is updated on every Members page role change; public.tenant_users was write
# once until PR #1287 taught accounts.Service.UpdateMemberRole to propagate onto
# it. platform.WorkspaceAdminGate authorizes off public.tenant_users, while the
# console's own page gate reads public.account_memberships, so a disagreement
# between them is an authorization defect in one of two directions:
#
#   stale grant  a public.tenant_users OWNER whose account membership is no
#                longer an active owner keeps feature gate, marketplace and
#                egress authority after an explicit demotion (issue #1245).
#   stale denial a business tenant's active account owner whose
#                public.tenant_users row still says MEMBER renders the admin
#                pages and is then answered 403 by the data fetch behind them,
#                landing on "Managed by your administrator" (issues #1244,
#                #1646).
#
# Both directions were corrected once by a migration (20260828_02 for the first,
# 20260901_01 for the second) and are kept corrected by the write side sync,
# which since issue #1646's review runs on BOTH writers of the authoritative
# column: accounts.Service.syncTenantRole is called from UpdateMemberRole (the
# Members page) and from AcceptInvitation (redeeming an owner or member
# invitation). That second call site is why this check can be a gate at all: a
# gate is only legitimate when the state it fails on has an automated path back
# to zero, and until that call existed an accepted owner invitation manufactured
# the failing state on current code with nothing to clear it.
#
# The sync is still best effort by design, logging and continuing when its
# statement matches nothing, so a failure leaves the two tables disagreeing with
# nothing but a log line to show for it. This check is the part that was
# missing. It reads the live data on every deploy, which is a thing somebody
# sees, unlike a log line nobody rereads after a run goes green.
#
# Only what can be repaired is allowed to fail. Four counted classes are printed
# and never fail, each because nothing in this repository can clear them:
# personal tenants holding MEMBER on purpose
# (apps/control-plane/internal/signup/personal_tenant.go's hardcode, so a self
# serve signup never reaches the workspace admin surfaces), ADMIN and VIEWER
# tiers that the migration deliberately declines to widen and that the sync's
# CASE deliberately passes through, divergences on archived (decommissioned)
# tenants, and ACTIVE tenant_users rows on tenants with no billing account
# mapping, which this check cannot resolve an owner for at all. Failing on any
# of those would block every deploy of every unrelated change until somebody
# hand edited the live database, which is how a gate gets routed around instead
# of obeyed.
#
# Exit status
# -----------
# 0 only when the two repairable classes are empty AND at least one pair was
# examined. Anything else is 1, including "could not tell": a check that cannot
# read the two tables, or that read them and saw nothing at all, has not checked
# anything, and unknown is not healthy.
#
# Failing this step fails the migrate job and stops the deploy, matching
# check-retention-schedule.sh. That coupling is deliberate. Either class means
# somebody currently holds, or is currently denied, workspace administrator
# authority that the product's own record says otherwise about, and a warning on
# a green run is indistinguishable from no warning at all.
#
# Connection settings come from libpq environment variables (PGHOST, PGPORT,
# PGUSER, PGDATABASE, PGPASSWORD), same as apply-migrations.sh. No DSN is built
# here, because DSN parameters are per driver and psql is a libpq client.
#
# PSQL_BIN names the client, same as apply-migrations.sh: on the demo box the
# database has no published port and exists only inside the stack's compose
# network, so the caller points this at scripts/stack-psql.sh.

set -euo pipefail

PSQL_BIN="${PSQL_BIN:-psql}"

psql_q() { "$PSQL_BIN" --no-psqlrc -qtAX -v ON_ERROR_STOP=1 "$@"; }

# ---------------------------------------------------------------------------
# Which database is this?
# ---------------------------------------------------------------------------
# Same guard, and the same reasoning, as check-retention-schedule.sh: a true
# answer about the wrong cluster reads as a true answer, and that shape hid an
# unscheduled retention job for weeks.
if [ -z "${HIVE_EXPECTED_DB_CLUSTER:-}" ]; then
  echo "::error::HIVE_EXPECTED_DB_CLUSTER is not set, so this check cannot prove which database it is reading. Set it to the system_identifier of the database the stack runs (the deploy workflow reads that from the supabase-db container)."
  exit 1
fi

if ! actual_cluster="$(psql_q -c 'SELECT system_identifier FROM pg_control_system()')"; then
  echo "::error::could not open a connection to check workspace role divergence, so the state of workspace administrator authority is unknown. Unknown is not healthy."
  exit 1
fi
actual_cluster="$(printf '%s' "$actual_cluster" | tr -d '[:space:]')"
if [ "$actual_cluster" != "$HIVE_EXPECTED_DB_CLUSTER" ]; then
  echo "::error::workspace role check is connected to cluster $actual_cluster but the stack's database is cluster $HIVE_EXPECTED_DB_CLUSTER. Refusing to report on a database the application does not use."
  exit 1
fi


# ---------------------------------------------------------------------------
# One snapshot, eight numbers
# ---------------------------------------------------------------------------
# Read in a single statement so the counts cannot describe eight different
# moments, and so "how many pairs did this even look at" is answered by the
# same query that answers "how many of them were wrong".
#
# Two of the eight fail. Five are printed and never fail, because nothing in
# this repository can clear them and a gate nobody can satisfy is a gate that
# gets routed around. The eighth is the examined count, and it fails when it
# is zero: a count of zero over zero rows prints exactly what a healthy box
# prints, which is the shape this repository has been bitten by repeatedly.
#
# The line between the two groups is one question, asked of every class:
# is there a code path in this repository that returns it to zero? If not, it
# is reported. Measured rather than assumed, against the live box on
# 2026-09-01, which is how orphan_owners below came to be its own class.
#
# What each one means, and why it is on the side of the line it is on:
#
#   examined            pairs of (tenant_users row, mapped account) considered.
#                       Zero is a failure: the query saw no data, so it proved
#                       nothing, whatever the other numbers say.
#   stale_grants        FAILS. tenant_users says OWNER while an ACTIVE
#                       account_memberships row for the same pair says
#                       something else. That is a demotion whose propagation
#                       did not land (issue #1245), and it is repairable:
#                       accounts.Service.syncTenantRole demotes it on the next
#                       role change, and 20260828_02 cleared the historical
#                       ones.
#   orphan_owners       PRINTED. tenant_users says OWNER and the mapped
#                       account has NO active membership for that user at all,
#                       so the account system has no opinion to propagate and
#                       syncTenantRole's join matches nothing (it reports
#                       no_match and returns). Nothing in this repository can
#                       clear it, so failing on it would block every deploy
#                       forever. Measured on the demo box on 2026-09-01: the
#                       two rows in this class are OWUI end to end identities
#                       seeded straight into tenant_users by
#                       scripts/seed-owui-e2e-user.py, which never writes
#                       account_memberships. Worth watching, not worth
#                       blocking a deploy on.
#   stale_denials       FAILS, and only for tenant_users role 'MEMBER'. That
#                       is the exact class
#                       20260901_01_tenant_users_role_promote_backfill.sql
#                       repairs and that syncTenantRole keeps repaired, so it
#                       is the only denial class with a path back to zero.
#   tier_diverged       PRINTED. ADMIN or VIEWER against an account owner. The
#                       migration deliberately declines to widen a chosen tier
#                       and SyncTenantMembershipRole's CASE ends in ELSE
#                       tu.role for the same reason, so nothing here can clear
#                       it and failing on it would block every deploy forever.
#   personal_by_design  PRINTED. A personal tenant's sole member holding
#                       MEMBER is signup/personal_tenant.go's deliberate
#                       hardcode, not drift.
#   archived_diverged   PRINTED. Either class on a soft deleted workspace.
#                       Nobody repairs roles on a decommissioned tenant, and
#                       both the migration and this check exclude archived
#                       tenants from the failing classes for that reason.
#   unmapped_active     PRINTED. ACTIVE tenant_users rows on a tenant with no
#                       public.tenant_billing_accounts row. They are invisible
#                       to every other number here, because the mapping is
#                       what resolves "which account's membership answers for
#                       this tenant", and EnsureTenantBillingAccount leaves a
#                       tenant unmapped whenever its members do not resolve to
#                       exactly one account. Printed so a green is honest
#                       about what it did not look at; 20260828_02 has the
#                       same blast radius.
read_counts() {
  psql_q -c "
    WITH pairs AS (
      SELECT tu.role AS tu_role,
             tu.status AS tu_status,
             t.personal_owner_user_id IS NOT NULL AS personal,
             t.archived_at IS NOT NULL AS archived,
             EXISTS (
               SELECT 1 FROM public.account_memberships am
                WHERE am.account_id = tba.account_id
                  AND am.user_id    = tu.user_id
                  AND am.role       = 'owner'
                  AND am.status     = 'active'
             ) AS account_owner,
             EXISTS (
               SELECT 1 FROM public.account_memberships am
                WHERE am.account_id = tba.account_id
                  AND am.user_id    = tu.user_id
                  AND am.status     = 'active'
             ) AS has_membership
        FROM public.tenant_users tu
        JOIN public.tenant_billing_accounts tba ON tba.tenant_id = tu.tenant_id
        JOIN public.tenants t ON t.id = tba.tenant_id
    )
    SELECT
      count(*),
      count(*) FILTER (WHERE tu_status = 'ACTIVE' AND NOT archived
                         AND tu_role = 'OWNER' AND NOT account_owner AND has_membership),
      count(*) FILTER (WHERE tu_status = 'ACTIVE' AND NOT archived
                         AND tu_role = 'OWNER' AND NOT has_membership),
      count(*) FILTER (WHERE tu_status = 'ACTIVE' AND NOT archived
                         AND tu_role = 'MEMBER' AND account_owner AND NOT personal),
      count(*) FILTER (WHERE tu_status = 'ACTIVE' AND NOT archived
                         AND tu_role IN ('ADMIN','VIEWER') AND account_owner AND NOT personal),
      count(*) FILTER (WHERE tu_status = 'ACTIVE' AND NOT archived
                         AND tu_role <> 'OWNER' AND account_owner AND personal),
      count(*) FILTER (WHERE tu_status = 'ACTIVE' AND archived
                         AND ((tu_role = 'OWNER' AND NOT account_owner)
                              OR (tu_role <> 'OWNER' AND account_owner AND NOT personal))),
      (SELECT count(*)
         FROM public.tenant_users u
        WHERE u.status = 'ACTIVE'
          AND NOT EXISTS (SELECT 1 FROM public.tenant_billing_accounts m
                           WHERE m.tenant_id = u.tenant_id))
      FROM pairs
  "
}

if ! counts="$(read_counts)"; then
  echo "::error::the workspace role divergence query failed, so whether anyone holds or is denied workspace administrator authority incorrectly is unknown. Unknown is not healthy."
  exit 1
fi

IFS='|' read -r examined stale_grants orphan_owners stale_denials tier_diverged \
  personal_by_design archived_diverged unmapped_active <<<"$(printf '%s' "$counts" | tr -d '[:space:]')"

for name in examined stale_grants orphan_owners stale_denials tier_diverged \
  personal_by_design archived_diverged unmapped_active; do
  if [ -z "${!name:-}" ]; then
    echo "::error::the workspace role divergence query returned no value for $name, so nothing was checked."
    exit 1
  fi
done

echo "workspace role divergence on cluster $actual_cluster: examined $examined mapped pair(s); stale grants $stale_grants; stale denials $stale_denials; tenant OWNERs with no account membership at all $orphan_owners (reported); ADMIN or VIEWER divergences $tier_diverged (reported); personal tenants holding MEMBER by design $personal_by_design; divergences on archived tenants $archived_diverged (reported); ACTIVE tenant_users rows on unmapped tenants $unmapped_active (outside this check entirely)"

failed=0

if [ "$examined" = "0" ]; then
  echo "::error::this check examined zero mapped (tenant_users, account) pairs, so its zeroes are the absence of data rather than the absence of divergence. public.tenant_billing_accounts joined to nothing: either this database has not been populated, or the mapping table has been renamed or repointed and this query no longer describes it."
  failed=1
fi

if [ "$stale_grants" != "0" ]; then
  echo "::error::$stale_grants public.tenant_users row(s) still carry role OWNER while the mapped account holds an ACTIVE membership for that user saying otherwise. Those users keep WorkspaceAdminGate and egress authority after an explicit demotion (issue #1245). accounts.Service.syncTenantRole demotes this on the next role change from the Members page; if it is not clearing, that sync is failing and its log line names the call site."
  failed=1
fi

if [ "$stale_denials" != "0" ]; then
  echo "::error::$stale_denials public.tenant_users MEMBER row(s) on a live business tenant deny a user public.account_memberships says is an active owner of the mapped account. Those owners see the console's 'Managed by your administrator' empty state on /console/feature-gates and /console/marketplace instead of their own pages (issues #1244, #1646). supabase/migrations/20260901_01_tenant_users_role_promote_backfill.sql cleared the rows that predate the write side sync, and accounts.Service.syncTenantRole now runs on both writers (Members page and invitation acceptance), so a row appearing after that is a sync failure rather than history."
  failed=1
fi

if [ "$failed" -ne 0 ]; then
  exit 1
fi

echo "Workspace administrator roles agree across public.tenant_users and public.account_memberships, over $examined examined pair(s)."
