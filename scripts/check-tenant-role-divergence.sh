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
# 20260901_01 for the second) and are kept corrected by the write side sync. A
# one time correction plus a write side sync is exactly what was in place when
# issue #1646 was filed, because the sync is best effort: it logs and moves on
# when its statement matches nothing, so a failure leaves the two tables
# disagreeing with nothing but a log line to show for it. This check is the part
# that was missing. It reads the live data on every deploy and fails, which is a
# thing somebody sees, unlike a log line nobody rereads after a run goes green.
#
# Deliberately NOT a failure: a personal (single user) tenant whose sole member
# holds MEMBER. apps/control-plane/internal/signup/personal_tenant.go hardcodes
# that on purpose so a self serve signup never reaches the workspace admin
# surfaces, even though that user owns their own billing account. Those rows are
# counted and printed, because they are the reason a personal tenant owner sees
# the same empty state a genuinely diverged owner does, and somebody reading
# this output should not have to rediscover that. Whether a personal tenant
# owner should administer their own workspace is a product decision, not drift.
#
# Exit status
# -----------
# 0 only when both divergence classes are empty. Anything else is 1, including
# "could not tell": a check that cannot read the two tables has not checked
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
# The two divergence classes, plus the by-design personal tenant count
# ---------------------------------------------------------------------------
# One query, three numbers, so the three counts are all read from the same
# snapshot and cannot describe three different moments.
#
# The predicates mirror supabase/migrations/20260901_01_tenant_users_role_promote_backfill.sql
# and 20260828_02_tenant_users_role_reconcile_backfill.sql. Keep them in step:
# a check that no longer describes what the migrations do is a check that goes
# green over the defect it was written for.
read_counts() {
  psql_q -c "
    WITH pairs AS (
      SELECT tu.role AS tu_role,
             tu.status AS tu_status,
             t.personal_owner_user_id IS NOT NULL AS personal,
             EXISTS (
               SELECT 1 FROM public.account_memberships am
                WHERE am.account_id = tba.account_id
                  AND am.user_id    = tu.user_id
                  AND am.role       = 'owner'
                  AND am.status     = 'active'
             ) AS account_owner
        FROM public.tenant_users tu
        JOIN public.tenant_billing_accounts tba ON tba.tenant_id = tu.tenant_id
        JOIN public.tenants t ON t.id = tba.tenant_id
    )
    SELECT
      count(*) FILTER (WHERE tu_status = 'ACTIVE' AND tu_role = 'OWNER' AND NOT account_owner),
      count(*) FILTER (WHERE tu_status = 'ACTIVE' AND tu_role <> 'OWNER' AND account_owner AND NOT personal),
      count(*) FILTER (WHERE tu_status = 'ACTIVE' AND tu_role <> 'OWNER' AND account_owner AND personal)
      FROM pairs
  "
}

if ! counts="$(read_counts)"; then
  echo "::error::the workspace role divergence query failed, so whether anyone holds or is denied workspace administrator authority incorrectly is unknown. Unknown is not healthy."
  exit 1
fi

IFS='|' read -r stale_grants stale_denials personal_by_design <<<"$(printf '%s' "$counts" | tr -d '[:space:]')"

if [ -z "${stale_grants:-}" ] || [ -z "${stale_denials:-}" ] || [ -z "${personal_by_design:-}" ]; then
  echo "::error::the workspace role divergence query returned no counts, so nothing was checked."
  exit 1
fi

echo "workspace role divergence on cluster $actual_cluster: stale grants $stale_grants, stale denials $stale_denials, personal tenants holding MEMBER by design $personal_by_design"

failed=0

if [ "$stale_grants" != "0" ]; then
  echo "::error::$stale_grants public.tenant_users row(s) still carry role OWNER for a user public.account_memberships no longer considers an active owner of the mapped account. Those users keep WorkspaceAdminGate and egress authority after an explicit demotion (issue #1245). accounts.Service.UpdateMemberRole's propagation is the only thing that clears this, so its sync is failing or was bypassed; re-issue the affected member's role from the Members page after fixing the cause."
  failed=1
fi

if [ "$stale_denials" != "0" ]; then
  echo "::error::$stale_denials public.tenant_users row(s) on a business tenant deny a user public.account_memberships says is an active owner of the mapped account. Those owners see the console's 'Managed by your administrator' empty state on /console/feature-gates and /console/marketplace instead of their own pages (issues #1244, #1646). supabase/migrations/20260901_01_tenant_users_role_promote_backfill.sql corrects rows that predate the write side sync; a row appearing after that migration means the sync itself is failing."
  failed=1
fi

if [ "$failed" -ne 0 ]; then
  exit 1
fi

echo "Workspace administrator roles agree across public.tenant_users and public.account_memberships."
