-- supabase/migrations/20260716_05_agent_tasks_service_scan.sql
--
-- The task status poller (issue #311 follow-up, SYNC_CONTRACT.md's Engine
-- seam section) needs to list active tasks across every tenant on an
-- interval. hive_app is NOT BYPASSRLS
-- (20260518_04_phase19_audit_rls_and_indexes.sql), so without this the
-- tenant-isolation policy on public.agent_tasks
-- (20260716_03_agent_tasks.sql) would return zero rows for a cross-tenant
-- scan: app.current_tenant_id is only ever set for one tenant at a time.
--
-- Mirrors audit_log_service_role_all's precedent exactly
-- (20260518_04_phase19_audit_rls_and_indexes.sql): PERMISSIVE policies OR
-- together, so this widens SELECT only. INSERT/UPDATE/DELETE still require
-- the correct app.current_tenant_id via the existing
-- agent_tasks_tenant_isolation policy — the poller's own writes go through
-- Repository.Transition, which already sets that per the task's own
-- tenant_id.
--
-- Depends on: 20260716_03_agent_tasks.sql.

BEGIN;

CREATE POLICY agent_tasks_service_scan
    ON public.agent_tasks AS PERMISSIVE FOR SELECT TO hive_app
    USING (true);

COMMIT;
