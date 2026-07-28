-- supabase/migrations/20260728_02_tenant_setting_key_enable_usage_metering.sql
-- Backend metering, Step 1 (schema only, no behaviour change). Adds the new
-- public.tenant_setting_key value the metering gate will read starting in a
-- later step. See vault spec-2026-07-28-backend-metering.md section 3.2.
--
-- ALTER TYPE ... ADD VALUE cannot run in the same transaction that then uses
-- the new value (Postgres restriction: a new enum label is not visible to
-- other commands in the same transaction that added it). This migration
-- does ONLY the ALTER TYPE and nothing else, in its own transaction, so no
-- dependent DML anywhere in this deploy can trip that restriction.

BEGIN;

ALTER TYPE public.tenant_setting_key ADD VALUE IF NOT EXISTS 'ENABLE_USAGE_METERING';

COMMIT;
