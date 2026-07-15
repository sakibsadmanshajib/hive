-- supabase/migrations/20260715_05_rag_rls_nullif_guard.sql
--
-- Fix #320: harden the rag_documents / rag_chunks RLS policies with the same
-- NULLIF(..., '')::uuid guard adopted by 20260715_03_egress_policy_ssot.sql.
--
-- 20260625_05_carl_rag.sql compares tenant_id directly against
-- current_setting('app.current_tenant_id', true)::uuid. Once a session has
-- called set_config('app.current_tenant_id', ..., true) at least once,
-- current_setting(name, true) on that same connection returns '' (not NULL)
-- after the LOCAL scope ends, rather than reverting to "never defined".
-- Casting '' straight to ::uuid raises invalid input syntax instead of
-- cleanly filtering rows, turning a bare/unscoped query on a reused pooled
-- connection into a 500 instead of a fail-closed empty result. NULLIF folds
-- that '' case to NULL first, so the comparison evaluates to NULL (Postgres
-- treats NULL as false) and access is denied -- fail closed either way,
-- never allow-all.
--
-- Does not edit the already-applied 20260625_05_carl_rag.sql; this migration
-- only replaces the two policies it created.

BEGIN;

DROP POLICY IF EXISTS rag_documents_tenant_isolation ON public.rag_documents;
CREATE POLICY rag_documents_tenant_isolation
    ON public.rag_documents AS PERMISSIVE FOR ALL TO hive_app
    USING      (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

DROP POLICY IF EXISTS rag_chunks_tenant_isolation ON public.rag_chunks;
CREATE POLICY rag_chunks_tenant_isolation
    ON public.rag_chunks AS PERMISSIVE FOR ALL TO hive_app
    USING      (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

COMMIT;
