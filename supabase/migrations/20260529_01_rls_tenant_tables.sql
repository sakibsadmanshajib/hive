-- =============================================================================
-- #107 — Row-Level Security on legacy tenant (account_*) tables
-- =============================================================================
-- Phase 19 already enabled RLS on the new tenant_* family (JWT tenant_id claim,
-- migration 20260518_04). The legacy account_* family — which still holds live
-- billing, credit-ledger, API-key, usage and file data — had NO RLS, so the
-- published Supabase anon/authenticated key could read every tenant's rows
-- (issue #107).
--
-- Threat model & mechanism:
--   * PostgREST connects as `authenticator` then SET ROLE anon|authenticated.
--     Neither role has BYPASSRLS, so enabling RLS blocks them except where a
--     policy grants access.
--   * The control-plane connects as the Supabase project `postgres` role, which
--     has BYPASSRLS — it is unaffected by these policies (FORCE included), so
--     all server-side billing/inference reads and writes continue to work.
--   * Customers never mutate these tables directly; all writes flow through the
--     control-plane. We therefore grant authenticated a SELECT-only policy
--     scoped to the caller's account memberships, and NO insert/update/delete
--     policy — direct PostgREST writes are denied.
--
-- Acceptance (#107): published anon key `select * from credit_ledger_entries`
-- returns 0 rows; control-plane reads/writes still succeed.
--
-- Idempotent: re-running drops and recreates each policy.
-- =============================================================================

begin;

-- Membership resolver. SECURITY DEFINER so it bypasses RLS on account_memberships
-- (avoids policy recursion) while staying scoped to the calling user. STABLE so
-- Postgres evaluates it once per statement, not per row.
create or replace function public.hive_current_account_ids()
returns setof uuid
language sql
stable
security definer
set search_path = public, pg_temp
as $$
  select account_id
    from public.account_memberships
   where user_id = (select auth.uid())
     and status = 'active'
$$;

revoke all on function public.hive_current_account_ids() from public;
grant execute on function public.hive_current_account_ids() to authenticated;

-- ---------------------------------------------------------------------------
-- Group A — tables keyed directly by account_id uuid -> accounts(id)
-- ---------------------------------------------------------------------------
do $$
declare
  t text;
  tables text[] := array[
    'account_profiles',
    'account_invitations',
    'account_billing_profiles',
    'account_budget_thresholds',
    'account_rate_policies',
    'api_keys',
    'api_key_events',
    'credit_idempotency_keys',
    'credit_ledger_entries',
    'credit_reservations',
    'payment_intents',
    'payment_invoices',
    'request_attempts',
    'usage_events'
  ];
begin
  foreach t in array tables loop
    execute format('alter table public.%I enable row level security', t);
    execute format('alter table public.%I force row level security', t);
    execute format('drop policy if exists %I on public.%I', t || '_tenant_select', t);
    execute format($f$
      create policy %I on public.%I
        for select to authenticated
        using (account_id in (select public.hive_current_account_ids()))
    $f$, t || '_tenant_select', t);
  end loop;
end $$;

-- ---------------------------------------------------------------------------
-- Group B — tables keyed by workspace_id uuid (workspace == account)
-- ---------------------------------------------------------------------------
do $$
declare
  t text;
  tables text[] := array['invoices', 'budgets', 'spend_alerts'];
begin
  foreach t in array tables loop
    execute format('alter table public.%I enable row level security', t);
    execute format('alter table public.%I force row level security', t);
    execute format('drop policy if exists %I on public.%I', t || '_tenant_select', t);
    execute format($f$
      create policy %I on public.%I
        for select to authenticated
        using (workspace_id in (select public.hive_current_account_ids()))
    $f$, t || '_tenant_select', t);
  end loop;
end $$;

-- ---------------------------------------------------------------------------
-- Group C — API-key child tables keyed by api_key_id -> api_keys(account_id)
-- ---------------------------------------------------------------------------
do $$
declare
  t text;
  tables text[] := array[
    'api_key_policies',
    'api_key_rate_policies',
    'api_key_budget_windows',
    'api_key_usage_rollups'
  ];
begin
  foreach t in array tables loop
    execute format('alter table public.%I enable row level security', t);
    execute format('alter table public.%I force row level security', t);
    execute format('drop policy if exists %I on public.%I', t || '_tenant_select', t);
    execute format($f$
      create policy %I on public.%I
        for select to authenticated
        using (api_key_id in (
          select id from public.api_keys
           where account_id in (select public.hive_current_account_ids())
        ))
    $f$, t || '_tenant_select', t);
  end loop;
end $$;

-- ---------------------------------------------------------------------------
-- Special-cased tables
-- ---------------------------------------------------------------------------

-- accounts: visible to members and to the owner.
alter table public.accounts enable row level security;
alter table public.accounts force row level security;
drop policy if exists accounts_tenant_select on public.accounts;
create policy accounts_tenant_select on public.accounts
  for select to authenticated
  using (
    id in (select public.hive_current_account_ids())
    or owner_user_id = (select auth.uid())
  );

-- account_memberships: a user sees memberships for accounts they belong to
-- (to list co-members) plus their own membership rows.
alter table public.account_memberships enable row level security;
alter table public.account_memberships force row level security;
drop policy if exists account_memberships_tenant_select on public.account_memberships;
create policy account_memberships_tenant_select on public.account_memberships
  for select to authenticated
  using (
    account_id in (select public.hive_current_account_ids())
    or user_id = (select auth.uid())
  );

-- credit_grants: keyed by granted_to_workspace_id, also visible to the grantee.
alter table public.credit_grants enable row level security;
alter table public.credit_grants force row level security;
drop policy if exists credit_grants_tenant_select on public.credit_grants;
create policy credit_grants_tenant_select on public.credit_grants
  for select to authenticated
  using (
    granted_to_workspace_id in (select public.hive_current_account_ids())
    or granted_to_user_id = (select auth.uid())
  );

-- credit_reservation_events -> credit_reservations(account_id)
alter table public.credit_reservation_events enable row level security;
alter table public.credit_reservation_events force row level security;
drop policy if exists credit_reservation_events_tenant_select on public.credit_reservation_events;
create policy credit_reservation_events_tenant_select on public.credit_reservation_events
  for select to authenticated
  using (reservation_id in (
    select id from public.credit_reservations
     where account_id in (select public.hive_current_account_ids())
  ));

-- credit_reconciliation_jobs -> credit_reservations(account_id)
alter table public.credit_reconciliation_jobs enable row level security;
alter table public.credit_reconciliation_jobs force row level security;
drop policy if exists credit_reconciliation_jobs_tenant_select on public.credit_reconciliation_jobs;
create policy credit_reconciliation_jobs_tenant_select on public.credit_reconciliation_jobs
  for select to authenticated
  using (reservation_id in (
    select id from public.credit_reservations
     where account_id in (select public.hive_current_account_ids())
  ));

-- payment_events -> payment_intents(account_id)
alter table public.payment_events enable row level security;
alter table public.payment_events force row level security;
drop policy if exists payment_events_tenant_select on public.payment_events;
create policy payment_events_tenant_select on public.payment_events
  for select to authenticated
  using (payment_intent_id in (
    select id from public.payment_intents
     where account_id in (select public.hive_current_account_ids())
  ));

-- Storage tables store the account UUID as text in account_id.
-- files
alter table public.files enable row level security;
alter table public.files force row level security;
drop policy if exists files_tenant_select on public.files;
create policy files_tenant_select on public.files
  for select to authenticated
  using (account_id in (select id::text from public.accounts where id in (select public.hive_current_account_ids())));

-- uploads
alter table public.uploads enable row level security;
alter table public.uploads force row level security;
drop policy if exists uploads_tenant_select on public.uploads;
create policy uploads_tenant_select on public.uploads
  for select to authenticated
  using (account_id in (select id::text from public.accounts where id in (select public.hive_current_account_ids())));

-- batches
alter table public.batches enable row level security;
alter table public.batches force row level security;
drop policy if exists batches_tenant_select on public.batches;
create policy batches_tenant_select on public.batches
  for select to authenticated
  using (account_id in (select id::text from public.accounts where id in (select public.hive_current_account_ids())));

-- upload_parts -> uploads(id) [text]
alter table public.upload_parts enable row level security;
alter table public.upload_parts force row level security;
drop policy if exists upload_parts_tenant_select on public.upload_parts;
create policy upload_parts_tenant_select on public.upload_parts
  for select to authenticated
  using (upload_id in (
    select id from public.uploads
     where account_id in (select id::text from public.accounts where id in (select public.hive_current_account_ids()))
  ));

-- batch_lines -> batches(id) [text]
alter table public.batch_lines enable row level security;
alter table public.batch_lines force row level security;
drop policy if exists batch_lines_tenant_select on public.batch_lines;
create policy batch_lines_tenant_select on public.batch_lines
  for select to authenticated
  using (batch_id in (
    select id from public.batches
     where account_id in (select id::text from public.accounts where id in (select public.hive_current_account_ids()))
  ));

commit;
