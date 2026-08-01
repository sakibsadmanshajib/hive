-- Issue #663: 20260801_11 normalized the release idempotency key in
-- public.credit_idempotency_keys only. public.credit_ledger_entries kept the
-- old amount-bearing "reservation:<id>:release-<credits>" key, so the two
-- tables that are supposed to agree on one key disagree.
--
-- The failure that asymmetry produces, reproduced on a scratch database during
-- the review of #662: a later flat-key release for a reservation whose ledger
-- entry still carries the old key takes the idempotency conflict branch, then
-- lookupExistingEntry searches credit_ledger_entries for the flat key, misses,
-- and returns ErrNotFound. That is a hard error where the design intends a
-- clean no-op. It does not double credit anyone, because it fails instead of
-- inserting, but it is a failure. It is unreachable on today's data, and the
-- thing that makes it unreachable is the remaining hold rather than the
-- reservation status: releaseLocked refuses only 'released' and 'finalized',
-- so the 6 'needs_reconciliation' reservations among the old-shape rows are
-- permitted past that check, and what actually stops them is
-- remainingHeldCredits returning zero, which gates the ledger call itself.
-- All 331 old-shape rows sit on reservations with zero remaining held credits
-- (reserved - consumed - released), so none can attempt another release. It
-- becomes reachable the moment a reservation with a non-zero remaining hold
-- carries an old-shape entry.
--
-- This migration changes the TEXT SHAPE OF A KEY ONLY. credits_delta,
-- created_at, entry_type, reservation_id and metadata are untouched, no row is
-- inserted and no row is deleted, so the ledger stays append-only in every
-- sense that matters: no financial amount moves and the row set is identical
-- before and after.
--
-- Collision analysis, checked live before writing this (read only, through the
-- transaction-mode pooler on 6543):
--   397 entry_type='reservation_release' rows: 331 in the old
--   release-<credits> shape, 66 already flat, 0 in any other shape.
--   Grouping those 397 rows by (account_id, normalized key) yields 0 groups
--   with more than one row. So no reservation carries two differing-amount
--   release entries, and no reservation carries both an old-shape and a
--   flat-shape entry. Normalizing therefore cannot collapse two distinct
--   historical entries into one today, and cannot violate
--   idx_credit_ledger_entries_account_idempotency (account_id, entry_type,
--   idempotency_key).
--
-- Both guards are kept anyway, because a row landing in a colliding position
-- between that check and this migration actually running must not be resolved
-- by losing an entry:
--   * the first NOT EXISTS skips an old-shape row whose flat key is already
--     taken by another entry;
--   * the second skips an old-shape row that shares its normalized key with
--     another old-shape row, which the first cannot see (the subquery reads the
--     statement's own snapshot, so two such rows would both pass it and then
--     collide on write).
-- A skipped row keeps its old key: nothing is overwritten and nothing is
-- deleted. The assertion at the bottom then fails the migration loudly rather
-- than leaving the asymmetry in place unannounced.
--
-- That abort is not all or nothing, deliberately. scripts/apply-migrations.sh
-- passes each file to psql without --single-transaction and this file carries
-- no BEGIN or COMMIT, so the UPDATE has already committed by the time the DO
-- block runs. A raise therefore keeps every safely normalized row normalized
-- and leaves only the skipped rows on the old key, which is what an operator
-- wants: reconcile the handful of colliding reservations by hand, re-run the
-- file, and the run completes. The file stays pending in
-- public.hive_schema_migrations either way, because the runner records a row
-- only after psql exits zero.
--
-- Re-runnable: the second run finds no old-shape rows, updates nothing, and
-- passes the assertion.

with old_shape as (
  select
    id,
    account_id,
    entry_type,
    regexp_replace(idempotency_key, '^(reservation:[0-9a-f-]+:release)-[0-9]+$', '\1') as flat_key
  from public.credit_ledger_entries
  where entry_type = 'reservation_release'
    and idempotency_key ~ '^reservation:[0-9a-f-]+:release-[0-9]+$'
),
safe as (
  select o.id, o.flat_key
  from old_shape o
  where not exists (
      select 1
      from public.credit_ledger_entries taken
      where taken.account_id = o.account_id
        and taken.entry_type = o.entry_type
        and taken.idempotency_key = o.flat_key
    )
    and not exists (
      select 1
      from old_shape dup
      where dup.account_id = o.account_id
        and dup.entry_type = o.entry_type
        and dup.flat_key = o.flat_key
        and dup.id <> o.id
    )
)
update public.credit_ledger_entries entry
set idempotency_key = safe.flat_key
from safe
where entry.id = safe.id;

do $$
declare
  remaining bigint;
begin
  select count(*)
  into remaining
  from public.credit_ledger_entries
  where entry_type = 'reservation_release'
    and idempotency_key ~ '^reservation:[0-9a-f-]+:release-[0-9]+$';

  if remaining > 0 then
    raise exception
      'issue #663: % reservation_release ledger rows still carry the old release-<credits> key shape. Normalizing them would collide with an entry that already holds the flat key, and this ledger is append-only, so the update was skipped instead of losing an entry. Reconcile those reservations by hand before re-running.',
      remaining;
  end if;
end
$$;
