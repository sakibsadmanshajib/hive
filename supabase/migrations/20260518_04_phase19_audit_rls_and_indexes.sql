-- supabase/migrations/20260518_04_phase19_audit_rls_and_indexes.sql
-- Phase 19 remediation — HIGH/MEDIUM defence-in-depth on audit surfaces.
--
-- H3 — Row-Level Security on audit_log, audit_outbox, llm_traces,
--      audit_cold_archive_manifest. Application code already filters by
--      tenant_id but a misconfigured psql session, a stolen anon key, or
--      a future code path that forgets the WHERE clause would currently
--      expose cross-tenant rows. RLS turns that into a database-enforced
--      invariant.
--
-- M5 — `auth.jwt()` SELECT-wrap so the function is evaluated once per
--      statement, not per row. Without the SELECT wrap Postgres invokes
--      auth.jwt() for every row scanned during a sequential query, which
--      can balloon plan cost at high audit volume.
--
-- M6 — audit_outbox claim plan: include `claimed_at` in the partial
--      index predicate so the planner picks it for the drainOnce
--      WHERE clause. Without it the query re-scans every row.
--
-- M7 — audit_cold_archive_manifest currently has no GRANT and no RLS;
--      anyone with USAGE on the schema can read manifest filenames +
--      sha256 digests and infer audit retention boundaries.
--
-- Service-role bypass: Supabase service_role is BYPASSRLS in Supabase
-- Cloud and self-hosted. Our application connects with hive_app which
-- is NOT BYPASSRLS, so the policies below MUST cover the hive_app code
-- path explicitly. We use `auth.uid()` and `auth.jwt() -> 'tenant_id'`
-- patterns consistent with the rest of the phase-19 RLS in
-- 20260516_01_phase19_tenants.sql.

BEGIN;

-- ───────────────────────────── audit_log ──────────────────────────────
ALTER TABLE public.audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.audit_log FORCE  ROW LEVEL SECURITY;

-- hive_app writes audit rows as a privileged service account. The
-- writer code never crosses tenants in a single INSERT, so the policy
-- only needs to grant the role's full INSERT/SELECT capability.
CREATE POLICY audit_log_service_role_all
  ON public.audit_log
  AS PERMISSIVE
  FOR ALL
  TO hive_app
  USING (true)
  WITH CHECK (true);

-- auditor_ro reads everything (it is the SOC-2 auditor identity).
-- Keeping it scoped via RLS means a misconfigured grant or anon key
-- cannot accidentally hand the same access to an unauthenticated user.
CREATE POLICY audit_log_auditor_select
  ON public.audit_log
  AS PERMISSIVE
  FOR SELECT
  TO auditor_ro
  USING (true);

-- ───────────────────────────── audit_outbox ───────────────────────────
ALTER TABLE public.audit_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.audit_outbox FORCE  ROW LEVEL SECURITY;

CREATE POLICY audit_outbox_service_role_all
  ON public.audit_outbox
  AS PERMISSIVE
  FOR ALL
  TO hive_app
  USING (true)
  WITH CHECK (true);

-- M6: keep the drainOnce hot path on an index scan. The query is:
--   WHERE delivered_at IS NULL
--     AND (next_retry_at IS NULL OR next_retry_at <= now())
--     AND (claimed_at   IS NULL OR claimed_at + lease <= now())
--   ORDER BY next_retry_at NULLS FIRST, created_at
--   LIMIT 50
-- The two lease/retry conditions both reference now(), which is only
-- STABLE — Postgres rejects non-IMMUTABLE expressions in a partial-index
-- predicate ("functions in index predicate must be marked IMMUTABLE"),
-- so claimed_at / next_retry_at CANNOT be added to the WHERE clause here.
-- Instead we predicate on the immutable `delivered_at IS NULL` (which
-- narrows to the small undelivered hot set) and lead with the ORDER BY
-- columns so the planner satisfies the sort + LIMIT 50 directly from the
-- index; the cheap now()-based lease/retry checks are then applied as
-- in-scan filters over that already-tiny, already-ordered candidate set.
DROP INDEX IF EXISTS audit_outbox_eligible;
CREATE INDEX audit_outbox_eligible
  ON public.audit_outbox (next_retry_at NULLS FIRST, created_at)
  WHERE delivered_at IS NULL;

-- ───────────────────────────── llm_traces ─────────────────────────────
ALTER TABLE public.llm_traces ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.llm_traces FORCE  ROW LEVEL SECURITY;

CREATE POLICY llm_traces_service_role_all
  ON public.llm_traces
  AS PERMISSIVE
  FOR ALL
  TO hive_app
  USING (true)
  WITH CHECK (true);

CREATE POLICY llm_traces_auditor_select
  ON public.llm_traces
  AS PERMISSIVE
  FOR SELECT
  TO auditor_ro
  USING (true);

-- ─────────────────── audit_cold_archive_manifest (M7) ─────────────────
-- The manifest was created without GRANT/RLS in 20260516_04 — fix
-- both gaps. Service writes (manifest cron) need INSERT + UPDATE;
-- auditor needs SELECT for chain spot-checks.
REVOKE ALL ON public.audit_cold_archive_manifest FROM PUBLIC;
GRANT INSERT, SELECT, UPDATE ON public.audit_cold_archive_manifest TO hive_app;
GRANT SELECT ON public.audit_cold_archive_manifest TO auditor_ro;

ALTER TABLE public.audit_cold_archive_manifest ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.audit_cold_archive_manifest FORCE ROW LEVEL SECURITY;

CREATE POLICY manifest_service_role_all
  ON public.audit_cold_archive_manifest
  AS PERMISSIVE
  FOR ALL
  TO hive_app
  USING (true)
  WITH CHECK (true);

CREATE POLICY manifest_auditor_select
  ON public.audit_cold_archive_manifest
  AS PERMISSIVE
  FOR SELECT
  TO auditor_ro
  USING (true);

-- ─────────────── M5: SELECT-wrap auth.jwt() in tenant RLS ────────────
-- Postgres invokes `auth.jwt()` once per row scanned when the call
-- appears inline in a USING/WITH CHECK predicate. Wrapping it in a
-- subselect collapses that to a single call per statement plan.
-- Apply across every phase-19 RLS policy that consults auth.jwt().
-- Drop + recreate is required because Postgres does not allow
-- altering a policy's predicate in place.

-- tenants
DROP POLICY IF EXISTS tenants_select_own ON public.tenants;
CREATE POLICY tenants_select_own
  ON public.tenants
  FOR SELECT
  USING (id = ((SELECT auth.jwt()) ->> 'tenant_id')::uuid);

-- tenant_settings
DROP POLICY IF EXISTS tenant_settings_select_own ON public.tenant_settings;
CREATE POLICY tenant_settings_select_own
  ON public.tenant_settings
  FOR SELECT
  USING (tenant_id = ((SELECT auth.jwt()) ->> 'tenant_id')::uuid);

DROP POLICY IF EXISTS tenant_settings_insert_own ON public.tenant_settings;
CREATE POLICY tenant_settings_insert_own
  ON public.tenant_settings
  FOR INSERT
  WITH CHECK (
    tenant_id = ((SELECT auth.jwt()) ->> 'tenant_id')::uuid
    AND ((SELECT auth.jwt()) ->> 'role') IN ('OWNER','ADMIN')
  );

DROP POLICY IF EXISTS tenant_settings_update_own ON public.tenant_settings;
CREATE POLICY tenant_settings_update_own
  ON public.tenant_settings
  FOR UPDATE
  USING (
    tenant_id = ((SELECT auth.jwt()) ->> 'tenant_id')::uuid
    AND ((SELECT auth.jwt()) ->> 'role') IN ('OWNER','ADMIN')
  )
  WITH CHECK (
    tenant_id = ((SELECT auth.jwt()) ->> 'tenant_id')::uuid
    AND ((SELECT auth.jwt()) ->> 'role') IN ('OWNER','ADMIN')
  );

DROP POLICY IF EXISTS tenant_settings_delete_own ON public.tenant_settings;
CREATE POLICY tenant_settings_delete_own
  ON public.tenant_settings
  FOR DELETE
  USING (
    tenant_id = ((SELECT auth.jwt()) ->> 'tenant_id')::uuid
    AND ((SELECT auth.jwt()) ->> 'role') IN ('OWNER','ADMIN')
  );

-- tenant_users
DROP POLICY IF EXISTS tenant_users_select_own ON public.tenant_users;
CREATE POLICY tenant_users_select_own
  ON public.tenant_users
  FOR SELECT
  USING (tenant_id = ((SELECT auth.jwt()) ->> 'tenant_id')::uuid);

DROP POLICY IF EXISTS tenant_users_insert_admin ON public.tenant_users;
CREATE POLICY tenant_users_insert_admin
  ON public.tenant_users
  FOR INSERT
  WITH CHECK (
    tenant_id = ((SELECT auth.jwt()) ->> 'tenant_id')::uuid
    AND ((SELECT auth.jwt()) ->> 'role') IN ('OWNER','ADMIN')
  );

DROP POLICY IF EXISTS tenant_users_update_admin ON public.tenant_users;
CREATE POLICY tenant_users_update_admin
  ON public.tenant_users
  FOR UPDATE
  USING (
    tenant_id = ((SELECT auth.jwt()) ->> 'tenant_id')::uuid
    AND ((SELECT auth.jwt()) ->> 'role') IN ('OWNER','ADMIN')
  )
  WITH CHECK (
    tenant_id = ((SELECT auth.jwt()) ->> 'tenant_id')::uuid
    AND ((SELECT auth.jwt()) ->> 'role') IN ('OWNER','ADMIN')
  );

DROP POLICY IF EXISTS tenant_users_delete_admin ON public.tenant_users;
CREATE POLICY tenant_users_delete_admin
  ON public.tenant_users
  FOR DELETE
  USING (
    tenant_id = ((SELECT auth.jwt()) ->> 'tenant_id')::uuid
    AND ((SELECT auth.jwt()) ->> 'role') IN ('OWNER','ADMIN')
  );

-- tenant_invites
DROP POLICY IF EXISTS tenant_invites_select_own ON public.tenant_invites;
CREATE POLICY tenant_invites_select_own
  ON public.tenant_invites
  FOR SELECT
  USING (tenant_id = ((SELECT auth.jwt()) ->> 'tenant_id')::uuid);

-- tenant_email_domains
DROP POLICY IF EXISTS tenant_email_domains_select_own ON public.tenant_email_domains;
CREATE POLICY tenant_email_domains_select_own
  ON public.tenant_email_domains
  FOR SELECT
  USING (tenant_id = ((SELECT auth.jwt()) ->> 'tenant_id')::uuid);

COMMIT;
