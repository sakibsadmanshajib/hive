-- Issue #917: the structural backstop under the flattened charge idempotency
-- key.
--
-- 20260829_03 plus the code change make a second charge for one reservation
-- impossible THROUGH THE APPLICATION, and they do it by making two attempts
-- compute the same key string. That guarantee lives in a Go format string. The
-- defect being fixed here is precisely what happens when such a guarantee is
-- expressed only in application code: the key was flattened once already, in
-- PR #912, and reverted, and in the meantime nothing in the database objected
-- to 4128 charges keyed on their own amounts. Application-level idempotency
-- with no database backstop is how this class of defect survives.
--
-- The existing unique index idx_credit_ledger_entries_account_idempotency is on
-- (account_id, entry_type, idempotency_key), so it only ever enforces what the
-- key TEXT already says. It was in place throughout and could not have stopped
-- any of this. This index states the invariant itself instead:
--
--   at most one usage_charge entry per (account_id, reservation_id)
--
-- which is D-034's capture-exactly-once written where a future refactor cannot
-- quietly weaken it. A regression that reintroduces an amount, or any other
-- variation, in the charge key now raises 23505 on the second insert instead of
-- billing a prepaid customer twice.
--
-- Partial on reservation_id IS NOT NULL because the column is nullable: a
-- usage_charge with no reservation is not part of this invariant and a NULL
-- would not be constrained by a plain unique index anyway. Restricted to
-- entry_type = 'usage_charge' because holds, releases and refunds legitimately
-- repeat per reservation.
--
-- Verified live read-only before writing this (2026-08-29): 0 reservations
-- carry more than one usage_charge entry across all 4128 of them, so this index
-- builds on today's data. If a duplicate does appear between that check and
-- this migration running, the build fails and the migration aborts, which is
-- the correct outcome: a ledger already holding a double charge needs an
-- operator, not an index quietly created around it.
--
-- CONCURRENTLY because credit_ledger_entries is on the live money path and a
-- plain CREATE UNIQUE INDEX takes an ACCESS EXCLUSIVE lock, which would block
-- every charge, hold and release for the duration of the build. CONCURRENTLY
-- cannot run inside a transaction block; scripts/apply-migrations.sh passes each
-- file to psql WITHOUT --single-transaction and this file carries no BEGIN or
-- COMMIT, so that is satisfied.
--
-- The cost of CONCURRENTLY is that a failed build leaves an INVALID index
-- behind, and IF NOT EXISTS would then happily skip re-creating it, leaving the
-- backstop permanently absent while every run reports success. The DO block
-- below drops exactly that leftover first, so a re-run after a partial
-- application actually rebuilds rather than silently accepting a dead index. A
-- VALID index of the same name is left alone, which is what makes the file
-- re-runnable.

do $$
begin
  if exists (
    select 1
    from pg_class idx
    join pg_index i on i.indexrelid = idx.oid
    join pg_namespace n on n.oid = idx.relnamespace
    where idx.relname = 'uq_credit_ledger_entries_one_charge_per_reservation'
      and n.nspname = 'public'
      and not i.indisvalid
  ) then
    raise notice 'issue #917: dropping an INVALID uq_credit_ledger_entries_one_charge_per_reservation left by an earlier interrupted CONCURRENTLY build, so it can be rebuilt below';
    execute 'drop index public.uq_credit_ledger_entries_one_charge_per_reservation';
  end if;
end
$$;

create unique index concurrently if not exists uq_credit_ledger_entries_one_charge_per_reservation
  on public.credit_ledger_entries (account_id, reservation_id)
  where entry_type = 'usage_charge' and reservation_id is not null;

comment on index public.uq_credit_ledger_entries_one_charge_per_reservation is
  'Issue #917: at most one usage_charge per (account_id, reservation_id). A reservation is one authorization and may be captured once (D-034). The application enforces this with the flat "reservation:<id>:charge" idempotency key; this index is the backstop that survives a refactor of that key.';
