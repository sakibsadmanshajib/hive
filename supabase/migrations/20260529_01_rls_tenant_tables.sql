-- =============================================================================
-- #107 — Row-Level Security on legacy tenant (account_*) tables
-- =============================================================================
-- Phase 19 already enabled RLS on the new tenant_* family (JWT tenant_id claim,
-- migration 20260518_04). The legacy account_* family — which still holds live
-- billing, credit-ledger, API-key, usage, FX and file data — had NO RLS, so the
-- published Supabase anon/authenticated key could read every tenant's rows
-- (issue #107).
--
-- Mechanism (mirrors the Phase 19 audit RLS in 20260518_04):
--   * The application (control-plane) connects as the `hive_app` role, which is
--     NOT BYPASSRLS (see 20260518_04 lines 24-26). Each table therefore gets an
--     explicit `FOR ALL TO hive_app USING(true) WITH CHECK(true)` policy so all
--     server-side reads/writes continue to work. (Where SUPABASE_DB_URL instead
--     runs as the BYPASSRLS `postgres` role, that role bypasses RLS regardless;
--     the hive_app policy covers the documented non-BYPASSRLS deployment.)
--   * PostgREST `anon`/`authenticated` roles have no BYPASSRLS and are granted
--     NO policy here, so they read 0 rows. This is intentional: customers never
--     touch these tables directly (the web-console has no direct PostgREST
--     reads — it goes through the control-plane API), and a blanket
--     member-readable SELECT would expose secrets such as api_keys.token_hash.
--
-- Acceptance (#107): published anon key `select * from credit_ledger_entries`
-- returns 0 rows; control-plane (hive_app) reads/writes still succeed.
--
-- Idempotent: re-running drops and recreates each policy.
-- =============================================================================

begin;

do $$
declare
  t text;
  -- Every per-tenant table in the legacy account_* family, plus account-scoped
  -- FX snapshots. Global reference tables (model_aliases, provider_*, routing
  -- policies) are intentionally excluded — they are not per-tenant.
  tables text[] := array[
    'accounts',
    'account_memberships',
    'account_profiles',
    'account_invitations',
    'account_billing_profiles',
    'account_budget_thresholds',
    'account_rate_policies',
    'api_keys',
    'api_key_events',
    'api_key_policies',
    'api_key_rate_policies',
    'api_key_budget_windows',
    'api_key_usage_rollups',
    'credit_grants',
    'credit_idempotency_keys',
    'credit_ledger_entries',
    'credit_reservations',
    'credit_reservation_events',
    'credit_reconciliation_jobs',
    'payment_intents',
    'payment_events',
    'payment_invoices',
    'invoices',
    'budgets',
    'spend_alerts',
    'request_attempts',
    'usage_events',
    'fx_snapshots',
    'files',
    'uploads',
    'upload_parts',
    'batches',
    'batch_lines'
  ];
begin
  foreach t in array tables loop
    execute format('alter table public.%I enable row level security', t);
    execute format('alter table public.%I force row level security', t);
    -- Control-plane (hive_app) full access; no policy for anon/authenticated.
    execute format('drop policy if exists %I on public.%I', t || '_service_role_all', t);
    execute format($f$
      create policy %I on public.%I
        for all
        to hive_app
        using (true)
        with check (true)
    $f$, t || '_service_role_all', t);
  end loop;
end $$;

commit;
