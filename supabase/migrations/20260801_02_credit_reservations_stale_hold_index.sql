-- Issue #616: serve the stranded-hold reaper's candidate scan.
--
-- The reaper looks for reservations still sitting in a non-terminal state past
-- a TTL. The existing index on (account_id, updated_at desc) cannot serve that
-- scan because the reaper has no account predicate to lead with.
--
-- The index is partial, so it only ever holds unsettled reservations and a row
-- leaves it as soon as the request settles. On a healthy system that is a
-- handful of rows regardless of how large the table grows.
--
-- Re-runnable: guarded per statement, and no data is written.
create index if not exists idx_credit_reservations_stale_holds
  on public.credit_reservations (created_at)
  where status in ('active', 'expanded');

comment on index public.idx_credit_reservations_stale_holds is
  'Serves the stranded-hold reaper candidate scan (issue #616).';
