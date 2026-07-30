-- supabase/migrations/20260729_01_metering_shadow_verdicts_created_at_index.sql
-- Metering Step 2 wave 3: retention support, part 1 of 2. See issue #603.
--
-- public.metering_shadow_verdicts (20260728_04) has two composite indexes,
-- neither of which leads with created_at:
--   idx_metering_shadow_verdicts_tenant_created  (tenant_id, created_at DESC)
--   idx_metering_shadow_verdicts_verdict_created  (verdict, created_at DESC)
-- An age-only DELETE (WHERE created_at < cutoff) cannot use either as a
-- leading-column index scan, so it would sequential-scan the whole table.
-- This plain btree on created_at alone is the index the nightly purge job
-- (20260729_02) actually needs, and it must exist BEFORE that job is
-- scheduled.
--
-- CREATE INDEX CONCURRENTLY cannot run inside a transaction block, so this
-- file deliberately has NO BEGIN/COMMIT (same class of restriction as the
-- ALTER TYPE ... ADD VALUE isolation in 20260728_02, different Postgres
-- rule, same fix: give the restricted statement its own untransacted file).
-- Required because metering_shadow_verdicts is a live table today (traffic
-- is currently zero since wave 3 has not wired a live write path yet, but
-- this table stops being safe to lock plainly the moment it does).

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_metering_shadow_verdicts_created_at
  ON public.metering_shadow_verdicts (created_at);
