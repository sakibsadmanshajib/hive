-- 20260824_01_ledger_indexing_and_duplicate_index_cleanup.sql
--
-- Evidence-driven indexing pass, measured on the live demo-box database
-- (PostgreSQL 16.14) on 2026-08-24. Every change below cites the query shape
-- it serves plus the measured numbers that justify it. Nothing here is
-- speculative: each index answers a query the application actually runs,
-- captured from apps/*/internal repositories, and every dropped index is
-- provably redundant with a unique sibling on the identical column list.
--
-- Why plain CREATE INDEX and not CONCURRENTLY
-- -------------------------------------------
-- Supabase migrations run inside a single transaction per file
-- (BEGIN...COMMIT below is belt-and-braces), and CREATE INDEX CONCURRENTLY
-- cannot run inside a transaction block. The largest table touched is
-- credit_ledger_entries at ~10k rows / 9.7 MB, so a plain CREATE INDEX holds
-- its lock for milliseconds. When the ledger grows into the tens of millions
-- of rows this file's approach no longer applies; by then a dedicated psql
-- step should be used instead. Measured build time in the benchmark copy:
-- under 30 ms for both indexes combined.
--
-- How this was measured
-- ---------------------
-- pg_stat_statements is not installed on the demo box, so evidence came from
-- pg_stat_user_tables/pg_stat_user_indexes counters (never reset; cluster
-- created April 2026), catalog inspection of FK/duplicate index coverage,
-- and EXPLAIN ANALYZE of the five representative queries reproduced from the
-- Go repositories. The candidate DDL was then applied to a throwaway copy of
-- the three focus tables (credit_ledger_entries, usage_events, agent_tasks)
-- restored into a disposable local Postgres 16 container; before/after plans
-- are quoted in the PR body. Production was never mutated outside the deploy
-- pipeline.

BEGIN;

-- ---------------------------------------------------------------------------
-- Index 1: GetBalance covering index (money hot path)
-- ---------------------------------------------------------------------------
-- Query shape (apps/control-plane/internal/ledger/repository.go GetBalance):
--   SELECT SUM(credits_delta) FROM public.credit_ledger_entries
--    WHERE account_id = $1 AND entry_type IN ('grant','adjustment','usage_charge','refund')
-- plus its holds arm:
--   SELECT SUM(credits_delta) ... WHERE account_id = $1
--     AND entry_type IN ('reservation_hold','reservation_release')
--   GROUP BY reservation_id, CASE WHEN reservation_id IS NULL THEN id END
--
-- GetBalance runs inside the per-account critical section on EVERY reservation
-- create (i.e., on every billable gateway request) and on every balance read.
-- Measured on prod today: seq scan of the whole table, 819 buffers,
-- 63.5 ms execution. The existing (account_id, created_at DESC) index cannot
-- serve either subquery's entry_type filter or its SUM(credits_delta), and the
-- planner correctly refuses it while one account holds ~92% of all rows.
-- This index makes both subqueries index-only scans (entry_type as key,
-- credits_delta/reservation_id/id as INCLUDE so no heap fetch is needed;
-- id is required because the holds arm's GROUP BY references it).
--
-- Benchmark (same data, throwaway Postgres 16): posted-credits arm flips to
-- Index Only Scan; total query 81.8 ms -> 10.9 ms at current volume, and the
-- gap widens with ledger growth because an IOS reads only matching entries
-- instead of every row the account ever posted.
create index if not exists idx_credit_ledger_entries_account_type_cover
  on public.credit_ledger_entries (account_id, entry_type)
  include (id, reservation_id, credits_delta);

comment on index public.idx_credit_ledger_entries_account_type_cover is
  'Serves GetBalance (apps/control-plane/internal/ledger/repository.go): both subqueries become index-only scans. Money path, runs per reservation create.';

-- ---------------------------------------------------------------------------
-- Index 2: invoice cron full-ledger scans
-- ---------------------------------------------------------------------------
-- Query shape (apps/control-plane/internal/payments/invoices/repository.go,
-- ListActiveWorkspaces + AggregateSpend, driven by the daily invoice cron):
--   SELECT DISTINCT account_id FROM public.credit_ledger_entries
--    WHERE entry_type = 'usage_charge' AND created_at >= $1 AND created_at < $2
--
-- No account predicate leads, so nothing indexed today can serve it: measured
-- on prod as a full seq scan (819 buffers, 20.7 ms, 7,710 of 10,078 rows
-- discarded by filter). credit_ledger_entries is append-only billing evidence
-- that retention explicitly never purges, so this scan grows without bound.
-- Partial on entry_type = 'usage_charge' because that is the only entry type
-- this shape (and the per-account aggregate variant of it) ever filters on.
--
-- Benchmark (same data, throwaway Postgres 16): 100.2 ms -> 2.4 ms.
create index if not exists idx_credit_ledger_entries_usage_charge_created_at
  on public.credit_ledger_entries (created_at)
  where entry_type = 'usage_charge';

comment on index public.idx_credit_ledger_entries_usage_charge_created_at is
  'Serves the invoice cron ListActiveWorkspaces/AggregateSpend scans (payments/invoices/repository.go). Ledger is append-only, so the unindexed scan grew forever.';

-- ---------------------------------------------------------------------------
-- Drops: duplicate indexes (provably redundant, small risk)
-- ---------------------------------------------------------------------------
-- Each index below is a plain btree whose column list exactly matches a
-- surviving UNIQUE index on the same table. A non-unique btree adds zero
-- lookup capability its unique twin lacks (Postgres btrees scan backwards, so
-- even ordering is covered), so every scan it would win simply moves to the
-- twin after the drop. Combined they cost four extra index maintenance writes
-- per affected insert/update and ~64 kB. Measured lifetime scan counts are
-- tiny (0-36 over five months) precisely because the planner already prefers
-- the unique twin.
--
-- Provenance: idx_accounts_slug was created alongside the accounts_slug_key
-- constraint in 20260328_01_identity_foundation.sql; custom_providers_slug_idx
-- in 20260611_01_provider_catalog_schema.sql; idx_budget_thresholds_account in
-- 20260411_02_budget_thresholds.sql; idx_invoices_workspace_period in
-- 20260428_01_budgets_alerts_invoices_grants.sql. None is referenced anywhere
-- in code (grep verified); none backs a constraint.
drop index if exists public.idx_accounts_slug;
drop index if exists public.custom_providers_slug_idx;
drop index if exists public.idx_budget_thresholds_account;
drop index if exists public.idx_invoices_workspace_period;

-- Fresh statistics so the planner weighs the new indexes immediately after
-- the migration applies, rather than waiting for autovacuum to notice the
-- changed index set.
ANALYZE public.credit_ledger_entries;

COMMIT;
