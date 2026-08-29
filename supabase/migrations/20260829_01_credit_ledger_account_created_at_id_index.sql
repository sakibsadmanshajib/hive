-- Issue #1367: serve the credit ledger's keyset pagination completely.
--
-- ListEntriesWithCursor now orders by (created_at DESC, id DESC), because `id`
-- is gen_random_uuid and ordering by it alone returned a customer's money
-- history shuffled. The existing idx_credit_ledger_entries_account_created_at
-- covers (account_id, created_at desc) and stops there, so the tie-breaker is
-- not in the index: within a group of entries sharing a created_at, Postgres
-- has to read the whole group and sort it by id before it can apply the LIMIT.
-- Entries are written one transaction at a time, so large tie groups are not
-- something this write path produces today, but the ordering the query asks
-- for should be one an index can hand it rather than one the planner has to
-- reconstruct on every page.
--
-- Superset of the old index on its leading columns, so nothing that used the
-- old one regresses. The old index is deliberately left in place: dropping it
-- is a separate decision, and this table is on the money path.
--
-- CONCURRENTLY, unlike 20260801_02's plain CREATE INDEX, because
-- credit_ledger_entries is written by every billable request and a plain build
-- takes ACCESS EXCLUSIVE for its whole duration, which would stall the money
-- path. scripts/apply-migrations.sh hands each file to psql with no wrapping
-- --single-transaction (see its Transactions section), so CONCURRENTLY, which
-- cannot run inside a transaction block, is available here. Do NOT add
-- BEGIN/COMMIT to this file.
--
-- The cost of CONCURRENTLY, stated rather than left to be discovered: a failed
-- build leaves an INVALID index behind, and because of the IF NOT EXISTS guard
-- a re-run would then skip it rather than repair it. If this file ever fails,
-- check for it and drop it before re-running:
--
--   SELECT i.indexrelid::regclass FROM pg_index i
--    WHERE NOT i.indisvalid
--      AND i.indexrelid::regclass::text = 'idx_credit_ledger_entries_account_created_at_id';
--   DROP INDEX IF EXISTS idx_credit_ledger_entries_account_created_at_id;
--
-- Re-runnable otherwise, and no data is written.
create index concurrently if not exists idx_credit_ledger_entries_account_created_at_id
  on public.credit_ledger_entries (account_id, created_at desc, id desc);

comment on index public.idx_credit_ledger_entries_account_created_at_id is
  'Serves the credit ledger keyset page (account_id, created_at DESC, id DESC), tie-breaker included (issue #1367).';
