-- Stage 1 of the Langfuse observability plan: the generation exporter's
-- cursor, plus the index that serves its keyset scan.
--
-- The exporter (apps/control-plane/internal/genexport) polls settled
-- public.usage_events joined to public.request_attempts and posts them to a
-- Langfuse ingestion endpoint. It runs after commit, in control-plane, never
-- in a request handler, so nothing here is on the hot path.
--
-- Nothing in this file is customer data. The cursor is one row holding one
-- position in a table this deployment already has.

-- ---------------------------------------------------------------------------
-- The cursor
-- ---------------------------------------------------------------------------
--
-- Single row, enforced by the `id boolean primary key default true check (id)`
-- idiom: `true` is the only value the check accepts and the primary key makes
-- it unique, so the table can hold exactly one row and a second insert
-- conflicts rather than creating a competing cursor.
--
-- Keyset, not offset: the position is (created_at, id), matching the index
-- below and the exporter's ORDER BY, so it is stable while rows are being
-- inserted underneath it.
create table if not exists public.generation_export_cursor (
  id              boolean     primary key default true check (id),
  last_created_at timestamptz not null,
  last_event_id   uuid        not null,
  updated_at      timestamptz not null default now()
);

comment on table public.generation_export_cursor is
  'Single-row keyset cursor for the control-plane generation exporter over public.usage_events. Carries no customer data.';

-- Seeded at the moment this migration is applied, deliberately, rather than at
-- the beginning of time. The exporter can be enabled months from now; seeding
-- at '-infinity' would make that first tick replay the entire usage history
-- into a second datastore in one go. A backfill is then an explicit, deliberate
-- act:
--
--   UPDATE public.generation_export_cursor
--      SET last_created_at = '2026-08-01T00:00:00Z',
--          last_event_id   = '00000000-0000-0000-0000-000000000000';
--
-- The all-zero uuid is the tie-breaker's lower bound. It is not a real event
-- id and never matches one, so a row sharing the seed timestamp is still
-- correctly ordered after it.
insert into public.generation_export_cursor (id, last_created_at, last_event_id)
values (true, now(), '00000000-0000-0000-0000-000000000000')
on conflict (id) do nothing;

-- No GRANT is needed here. 20260828_01_service_role_public_schema_grant.sql
-- installed ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public, so a
-- table created by postgres already carries the service_role grants. `anon`
-- and `authenticated` are deliberately not granted anything on it, matching
-- every other internal accounting table: nothing customer-facing reads this.

-- ---------------------------------------------------------------------------
-- The index that serves the exporter's scan
-- ---------------------------------------------------------------------------
--
-- The exporter reads across all accounts ordered by (created_at, id), and the
-- two existing indexes on this table are both account-scoped
-- (idx_usage_events_account_created_at, idx_usage_events_attempt_created_at),
-- so neither can serve it. Without this index the poll degrades to a full scan
-- of usage_events every five seconds.
--
-- Partial on the terminal event types, which is the only set the exporter ever
-- reads (D-034: an in-flight charge is never published as if it were final).
-- The predicate matches the exporter's WHERE clause literally, so the planner
-- can use it, and it keeps the index off the high-churn `stream_update` rows.
-- Keep this list and genexport.TerminalEventTypes in step.
--
-- CONCURRENTLY, following 20260829_01's precedent, because usage_events is
-- written by every billable request and a plain CREATE INDEX holds ACCESS
-- EXCLUSIVE for the whole build, which would stall the money path.
-- scripts/apply-migrations.sh hands each file to psql with no wrapping
-- --single-transaction, so CONCURRENTLY, which cannot run inside a transaction
-- block, is available. Do NOT add BEGIN/COMMIT to this file.
--
-- The cost of CONCURRENTLY, stated rather than left to be discovered: a failed
-- build leaves an INVALID index behind, and the IF NOT EXISTS guard would then
-- make a re-run skip it rather than repair it. If this file ever fails, check
-- for it and drop it before re-running:
--
--   SELECT i.indexrelid::regclass FROM pg_index i
--    WHERE NOT i.indisvalid
--      AND i.indexrelid::regclass::text = 'idx_usage_events_settled_created_at_id';
--   DROP INDEX IF EXISTS public.idx_usage_events_settled_created_at_id;
create index concurrently if not exists idx_usage_events_settled_created_at_id
  on public.usage_events (created_at, id)
  where event_type in ('completed', 'released', 'refunded', 'error', 'reconciled');

comment on index public.idx_usage_events_settled_created_at_id is
  'Serves the generation exporter keyset scan over settled usage events, ordered (created_at, id).';
