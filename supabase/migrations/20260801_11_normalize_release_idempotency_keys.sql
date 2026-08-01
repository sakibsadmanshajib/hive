-- Issue #652: reservation release idempotency keys used two different shapes.
-- finalizeLocked's partial release (charge some, release the remainder) keyed
-- on "reservation:<id>:release-<credits>"; releaseLocked's full release
-- (edge-api's settlement fallback, and the #648 reaper) already keyed on the
-- flat "reservation:<id>:release". A key that varies with the amount cannot
-- deduplicate two release attempts of DIFFERING amounts for the same
-- reservation, which is exactly the case worth catching, so the code fix
-- (same commit) makes finalizeLocked use the flat shape too.
--
-- Transition safety: a reservation already fully settled (status finalized,
-- or remaining held credits already zero) can never attempt a second release
-- regardless of key shape -- releaseLocked refuses a finalized reservation
-- outright, and remainingHeldCredits() gates the ledger call itself once the
-- prior charge+release already accounts for the whole hold. The one gap the
-- code fix alone would leave open: a reservation whose FIRST release was
-- already posted under the OLD shape before this migration runs would not be
-- protected by the new flat key, so a later differing-amount release attempt
-- (the reaper, most plausibly) would not collide with it and could post a
-- second entry. This migration closes that gap by normalizing every existing
-- old-shape row to the new flat key, so a historical reservation is covered
-- by the same guard a freshly-created one gets.
--
-- Checked live before writing this (read only, transaction-mode pooler):
-- 291 rows in the old release-<credits> shape, 44 already in the flat
-- shape, zero reservations carrying both -- so this UPDATE cannot violate
-- the (account_id, operation_type, idempotency_key) primary key today. The
-- WHERE NOT EXISTS guard is kept anyway so a row landing in both shapes
-- between the check above and this migration actually running is skipped
-- rather than erroring, and so the statement is safely re-runnable: the
-- second run finds no more old-shape rows to match.

update public.credit_idempotency_keys old
set idempotency_key = regexp_replace(old.idempotency_key, '^(reservation:[0-9a-f-]+:release)-[0-9]+$', '\1')
where old.operation_type = 'reservation_release'
  and old.idempotency_key ~ '^reservation:[0-9a-f-]+:release-[0-9]+$'
  and not exists (
    select 1
    from public.credit_idempotency_keys flat
    where flat.account_id = old.account_id
      and flat.operation_type = old.operation_type
      and flat.idempotency_key = regexp_replace(old.idempotency_key, '^(reservation:[0-9a-f-]+:release)-[0-9]+$', '\1')
  );
