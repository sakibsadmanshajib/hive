-- Issue #917: the charge idempotency key carried its own amount,
-- "reservation:<id>:charge-<credits>", so two settlement attempts for the same
-- reservation that disagree on the amount wrote two different keys and
-- therefore two charges. An idempotency key exists precisely so a retry cannot
-- charge twice; keying it on the amount protected only the retry that computed
-- the same number, which is the retry that never needed protecting.
--
-- The code fix in the same commit flattens the key to "reservation:<id>:charge",
-- the way 20260801_11/12 flattened the release key for issues #652 and #663.
-- This migration is the half that makes the flatten a fix instead of a
-- regression: until the existing rows are normalized, a retry that crossed the
-- deploy would look for the flat key, miss the old amount-bearing entry, and
-- insert a SECOND charge beside it, turning a replay that is a clean no-op
-- today into a double bill. That is why flattening the key alone was reverted
-- in PR #912.
--
-- BOTH tables in one file, deliberately. Issue #663 is the record of what
-- happens when only one is done: 20260801_11 normalized
-- public.credit_idempotency_keys and left public.credit_ledger_entries on the
-- old shape, so the two tables that are supposed to agree on one key disagreed,
-- and PostEntryTx's conflict branch looked up a key that was not there and
-- returned ErrNotFound. Splitting them again would re-earn that lesson.
--
-- This migration changes the TEXT SHAPE OF A KEY ONLY. credits_delta,
-- created_at, entry_type, operation_type, reservation_id, request_id,
-- attempt_id and metadata are untouched, no row is inserted and no row is
-- deleted, so the ledger stays append-only in every sense that matters: no
-- financial amount moves and the row set is identical before and after. In
-- particular the 17 rows known to be overcharged by settlement defects fixed on
-- 2026-08-28 are left exactly as they are; correcting those is an owner-decided
-- compensating grant, not a silent rewrite of an append-only ledger.
--
-- Collision analysis, checked live read-only against the self-hosted Postgres
-- immediately before writing this file (2026-08-29):
--   4128 entry_type='usage_charge' rows in public.credit_ledger_entries: ALL
--   4128 in the old charge-<credits> shape, 0 already flat, 0 in any other
--   shape.
--   Grouping those old-shape rows by (account_id, normalized key) yields 0
--   groups with more than one row, and 0 of them have their flat key already
--   taken by another entry. 0 reservations carry more than one usage_charge
--   entry at all.
-- So no reservation carries two differing-amount charge entries, and none
-- carries both an old-shape and a flat-shape entry. Normalizing therefore
-- cannot collapse two distinct historical entries into one today, and cannot
-- violate idx_credit_ledger_entries_account_idempotency (account_id,
-- entry_type, idempotency_key) or credit_idempotency_keys' primary key
-- (account_id, operation_type, idempotency_key).
--
-- Both guards are kept anyway, because a row landing in a colliding position
-- between that check and this migration actually running must not be resolved
-- by losing an entry:
--   * the first NOT EXISTS skips an old-shape row whose flat key is already
--     taken by another row;
--   * the second skips an old-shape row that shares its normalized key with
--     another old-shape row, which the first cannot see (the subquery reads the
--     statement's own snapshot, so two such rows would both pass it and then
--     collide on write).
-- A skipped row keeps its old key: nothing is overwritten and nothing is
-- deleted. The assertion at the bottom then fails the migration loudly rather
-- than leaving the asymmetry in place unannounced.
--
-- Safe under partial application. scripts/apply-migrations.sh passes each file
-- to psql with ON_ERROR_STOP=1 and WITHOUT --single-transaction, and this file
-- carries no BEGIN or COMMIT, so each statement commits on its own and an abort
-- can leave one table normalized and the other not. That asymmetry fails
-- CLOSED in both directions and moves no money either way: with the keys table
-- normalized first, a retry takes the conflict branch and then misses in
-- credit_ledger_entries, returning ErrNotFound; with the ledger normalized
-- first, the retry inserts a fresh key row and then violates the unique index
-- on credit_ledger_entries. Both are hard errors that refuse the settlement,
-- neither is a second charge. An operator reconciles the handful of colliding
-- reservations by hand and re-runs the file. The file stays pending in
-- public.hive_schema_migrations either way, because the runner records a row
-- only after psql exits zero.
--
-- Re-runnable: the second run finds no old-shape rows, updates nothing, and
-- passes both assertions.

-- 1. public.credit_idempotency_keys, which is what PostEntryTx's
--    "INSERT ... ON CONFLICT DO NOTHING" actually reads to decide whether a
--    charge has already been posted.
update public.credit_idempotency_keys old
set idempotency_key = regexp_replace(old.idempotency_key, '^(reservation:[0-9a-f-]+:charge)-[0-9]+$', '\1')
where old.operation_type = 'usage_charge'
  and old.idempotency_key ~ '^reservation:[0-9a-f-]+:charge-[0-9]+$'
  and not exists (
    select 1
    from public.credit_idempotency_keys flat
    where flat.account_id = old.account_id
      and flat.operation_type = old.operation_type
      and flat.idempotency_key = regexp_replace(old.idempotency_key, '^(reservation:[0-9a-f-]+:charge)-[0-9]+$', '\1')
  )
  and not exists (
    select 1
    from public.credit_idempotency_keys dup
    where dup.account_id = old.account_id
      and dup.operation_type = old.operation_type
      and dup.idempotency_key <> old.idempotency_key
      and dup.idempotency_key ~ '^reservation:[0-9a-f-]+:charge-[0-9]+$'
      and regexp_replace(dup.idempotency_key, '^(reservation:[0-9a-f-]+:charge)-[0-9]+$', '\1')
          = regexp_replace(old.idempotency_key, '^(reservation:[0-9a-f-]+:charge)-[0-9]+$', '\1')
  );

-- 2. public.credit_ledger_entries, which is what lookupExistingEntry reads to
--    hand the deduplicated entry back to the caller. The two must agree.
with old_shape as (
  select
    id,
    account_id,
    entry_type,
    regexp_replace(idempotency_key, '^(reservation:[0-9a-f-]+:charge)-[0-9]+$', '\1') as flat_key
  from public.credit_ledger_entries
  where entry_type = 'usage_charge'
    and idempotency_key ~ '^reservation:[0-9a-f-]+:charge-[0-9]+$'
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
  remaining_keys bigint;
  remaining_entries bigint;
begin
  select count(*)
  into remaining_keys
  from public.credit_idempotency_keys
  where operation_type = 'usage_charge'
    and idempotency_key ~ '^reservation:[0-9a-f-]+:charge-[0-9]+$';

  select count(*)
  into remaining_entries
  from public.credit_ledger_entries
  where entry_type = 'usage_charge'
    and idempotency_key ~ '^reservation:[0-9a-f-]+:charge-[0-9]+$';

  if remaining_keys > 0 or remaining_entries > 0 then
    raise exception
      'issue #917: % credit_idempotency_keys row(s) and % credit_ledger_entries row(s) still carry the old charge-<credits> key shape. Normalizing them would collide with a row that already holds the flat key, and this ledger is append-only, so the update was skipped instead of losing an entry. Reconcile those reservations by hand before re-running.',
      remaining_keys, remaining_entries;
  end if;
end
$$;
