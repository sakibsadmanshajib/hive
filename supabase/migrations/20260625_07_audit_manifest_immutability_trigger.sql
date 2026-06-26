-- supabase/migrations/20260625_07_audit_manifest_immutability_trigger.sql
--
-- Enforce write-once immutability on audit_cold_archive_manifest at the DB layer.
-- RLS alone does not protect against the service role, which bypasses it.
-- A trigger fires for ALL roles (including service role) and blocks:
--   - Any UPDATE of a manifest row before its purge_after timestamp.
--   - Any DELETE of a manifest row before its purge_after timestamp.
--
-- The ONLY legitimate delete is the retention-expiry purge after 10 years
-- (purge_after <= now()), which PurgeExpired executes. Even that path goes
-- through this trigger; the trigger allows it because purge_after <= now().
--
-- There is no legitimate UPDATE path: manifests are immutable once written.
-- Any UPDATE attempt (before or after purge_after) raises an exception.

BEGIN;

CREATE OR REPLACE FUNCTION public.audit_manifest_immutability()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION
            'audit_cold_archive_manifest is write-once: UPDATE not permitted (manifest id=%, tenant_id=%, partition_month=%)',
            OLD.id, OLD.tenant_id, OLD.partition_month;
    END IF;

    -- DELETE: only allowed once purge_after has passed.
    IF TG_OP = 'DELETE' THEN
        IF OLD.purge_after > now() THEN
            RAISE EXCEPTION
                'audit_cold_archive_manifest: DELETE before purge_after is forbidden '
                '(manifest id=%, purge_after=%, now=%)',
                OLD.id, OLD.purge_after, now();
        END IF;
    END IF;

    RETURN OLD;
END;
$$;

-- BEFORE trigger so the exception fires before any row is mutated.
DROP TRIGGER IF EXISTS audit_manifest_immutability_trg
    ON public.audit_cold_archive_manifest;

CREATE TRIGGER audit_manifest_immutability_trg
    BEFORE UPDATE OR DELETE
    ON public.audit_cold_archive_manifest
    FOR EACH ROW
    EXECUTE FUNCTION public.audit_manifest_immutability();

COMMENT ON FUNCTION public.audit_manifest_immutability() IS
    'Enforces write-once immutability on audit_cold_archive_manifest. '
    'UPDATE is never allowed. DELETE is only allowed after purge_after (PHIPA 10-year). '
    'Fires for ALL roles including service_role, bypassing RLS limitations.';

COMMIT;
