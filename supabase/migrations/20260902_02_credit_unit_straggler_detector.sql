-- =============================================================================
-- Credit unit straggler detector, corrected (issue #1704).
--
-- WHAT WAS WRONG
--   20260823_40_credit_unit_rescale_billion.sql shipped a post-deploy STRAGGLER
--   DETECTOR whose predicate was "nonzero and carries no credit_unit key", and
--   a documented remedy next to it: multiply every match by 10,000. On the live
--   demo box that query returns seven rows holding 221,002,000,000 credits, and
--   every one of them is a correctly scaled grant written AFTER the rescale by
--   seed or operator SQL, which never reaches ledger.stampCreditUnit because
--   stamping lives in the Go writer (apps/control-plane/internal/ledger/
--   repository.go PostEntry), not in the schema. Nothing in the data was wrong.
--   The instruction was, and the next person investigating a credit anomaly is
--   exactly the person who would have found it and run it.
--
--   A MISSING STAMP IS NOT EVIDENCE OF AN OLD UNIT. It is evidence of a writer
--   that does not stamp.
--
-- WHAT ACTUALLY SEPARATES THE TWO POPULATIONS
--   When the row was written, against public.credit_unit_rescale.applied_at,
--   which is the rescale's own clock_timestamp() upper bound and the only
--   authority in the database on when the unit changed. Read, never assumed and
--   never parsed out of a filename (the file named 20260823 ran at
--   2026-08-24T00:22:12Z on the box).
--
--   The comparison runs in OPPOSITE directions on the two tables the old
--   runbook queried, which is the part that is easy to get backwards:
--
--     credit_ledger_entries and credit_reservation_events
--       20260823_40 stamped every row it scaled ('legacy-1usd-100k-credits'),
--       so an unstamped nonzero row from BEFORE the boundary is one the scan
--       missed: a genuine candidate. After the boundary it is an unstamped
--       writer, not an unscaled row.
--     payment_intents
--       20260823_40 scaled every intent and stamped NONE of them (its step 6
--       has no metadata clause at all), so on this table the pre-boundary
--       unstamped rows are the correctly scaled ones, and only an unstamped row
--       written inside the recreate window that FOLLOWS the boundary can be an
--       old-binary straggler.
--
--   The one genuinely ambiguous window is the container recreate that follows a
--   migration: old binaries keep serving for seconds to minutes after COMMIT
--   and stamp nothing, so a row they wrote lands just after the boundary,
--   unscaled and unflagged. One hour is a deliberately generous upper bound on
--   that window; rows inside it stay candidates for a human to reconcile.
--
-- WHAT THIS FILE DOES
--   1. public.credit_unit_straggler_candidates, the corrected detector, as a
--      view so there is ONE definition rather than a query pasted into a
--      comment that goes stale the moment a writer changes. A hit is a
--      CANDIDATE, not a verdict, and there is no blanket remedy: see the view
--      comment.
--   2. Stamps the rows whose unit is already known, so the view returns clean
--      and a future reader is not left judging whether seven hits are the known
--      false positives or a new problem. Metadata only. No amount is written by
--      this file, and TestStragglerMigrationWritesMetadataOnly fails if one
--      ever is.
--
-- WHAT THIS FILE DELIBERATELY DOES NOT DO
--   It does not stamp post-boundary credit_reservation_events (7,948 of them on
--   the box). Those are unstamped because apps/control-plane/internal/
--   accounting/repository.go does not stamp, and a stamp is a CLAIM BY THE
--   WRITER about which unit it speaks. Asserting it on a writer's behalf, here
--   or in a trigger, manufactures an audit record nobody made. The detector
--   above reads them correctly without it, and closing the writer gap is a
--   change to those writers, tracked separately.
-- =============================================================================

BEGIN;

SET LOCAL lock_timeout = '5s';

-- security_invoker: this view reads three money tables, and without it a view
-- runs with its owner's privileges and hands anything granted SELECT on it a
-- way around their row level security. With it, the caller's own policies
-- apply. The revokes below are the second half of the same lockdown, shaped
-- like public.credit_unit_rescale's own and guarded on role existence so this
-- file also applies to a plain Postgres (CI throwaway) with no Supabase roles.
CREATE OR REPLACE VIEW public.credit_unit_straggler_candidates
    WITH (security_invoker = true) AS
WITH boundary AS (
    -- An aggregate over the marker table, so this subquery returns exactly one
    -- row even when the marker is absent. A plain SELECT would return none, the
    -- joins below would return none either, and a database whose boundary is
    -- UNKNOWN would report a clean detector: a silent absence read as a
    -- negative answer. With NULL the predicates below open up instead and every
    -- unstamped row becomes a candidate, which is loud and fails toward review.
    SELECT max(applied_at) AS applied_at FROM public.credit_unit_rescale WHERE id = 1
)
SELECT 'credit_ledger_entries'::text AS source_table,
       e.id                          AS row_id,
       e.account_id                  AS account_id,
       e.credits_delta               AS credits,
       e.created_at                  AS created_at,
       b.applied_at                  AS rescale_applied_at
  FROM public.credit_ledger_entries e
  CROSS JOIN boundary b
 WHERE e.credits_delta <> 0
   AND NOT (e.metadata ? 'credit_unit')
   AND (b.applied_at IS NULL OR e.created_at <= b.applied_at + interval '1 hour')
UNION ALL
SELECT 'credit_reservation_events'::text,
       ev.id,
       r.account_id,
       ev.credits_delta,
       ev.created_at,
       b.applied_at
  FROM public.credit_reservation_events ev
  JOIN public.credit_reservations r ON r.id = ev.reservation_id
  CROSS JOIN boundary b
 WHERE ev.credits_delta <> 0
   AND NOT (ev.metadata ? 'credit_unit')
   AND (b.applied_at IS NULL OR ev.created_at <= b.applied_at + interval '1 hour')
UNION ALL
SELECT 'payment_intents'::text,
       i.id,
       i.account_id,
       i.credits,
       i.created_at,
       b.applied_at
  FROM public.payment_intents i
  CROSS JOIN boundary b
 WHERE i.credits <> 0
   AND NOT (i.metadata ? 'credit_unit')
   -- Bounded at both ends, like the ledger arm and for the same reason. The
   -- lower bound is what makes this table's rule the inverse of the ledger's;
   -- the upper bound is this file's own argument applied to itself. An
   -- unstamped intent written days later is a writer that does not stamp, not
   -- an old unit, and leaving it a permanent candidate would rebuild the false
   -- positive this migration exists to remove, on the other table.
   AND (b.applied_at IS NULL
        OR (i.created_at > b.applied_at
            AND i.created_at <= b.applied_at + interval '1 hour'));

COMMENT ON VIEW public.credit_unit_straggler_candidates IS
    'Rows that MIGHT still be denominated in the pre-rescale credit unit (1 USD = 100,000 credits), for migration 20260823_40. A row appears here only if its own table says it could be old: unstamped and written at or before public.credit_unit_rescale.applied_at (plus one hour of container-recreate window) for credit_ledger_entries and credit_reservation_events, which the rescale stamped as it scaled; unstamped and written INSIDE that one hour window after the boundary for payment_intents, which the rescale scaled without stamping any of them, so its pre-boundary rows are the correctly scaled ones. Both arms end at the same instant: an unstamped row written later than that is a writer that does not stamp, on either table. Zero rows is the expected state. THERE IS NO BLANKET REMEDY: a hit is a candidate, not a verdict, and multiplying it by 10,000 without first reconciling that one account against public.credit_ledger_entries is how issue #1704 nearly inflated 221 billion credits of real grants. Reconcile per account, in the shape apps/control-plane/internal/payments/invoices/rescale_repair.go uses: ask the ledger what the figure is worth, refuse to write anything the ledger does not support, and correct one row at a time under a guard pinning the value you read. An empty result from a database with no marker row is impossible by construction: the boundary subquery yields NULL there and every unstamped row becomes a candidate.';

do $lockdown$
begin
  if exists (select 1 from pg_roles where rolname = 'anon') then
    execute 'revoke all on public.credit_unit_straggler_candidates from anon';
  end if;
  if exists (select 1 from pg_roles where rolname = 'authenticated') then
    execute 'revoke all on public.credit_unit_straggler_candidates from authenticated';
  end if;
end $lockdown$;

-- ---------------------------------------------------------------------------
-- Stamp the rows whose unit is already known.
--
-- Metadata only. Every UPDATE below sets metadata and nothing else, so no
-- balance, hold, charge or purchase moves by one credit. credit_unit_stamped_by
-- records that the stamp came from this backfill rather than from the row's own
-- writer, because a stamp asserted after the fact is weaker evidence than one
-- the writer made and an auditor should be able to tell them apart.
--
-- Idempotent: both predicates require the key to be ABSENT, so a second run
-- matches nothing. Safe on a fresh database, where the rescale marker was
-- inserted seconds ago and neither predicate can match anything at all.
-- ---------------------------------------------------------------------------
DO $stamp$
DECLARE
    boundary timestamptz;
    stamped  bigint;
BEGIN
    SELECT applied_at INTO boundary FROM public.credit_unit_rescale WHERE id = 1;
    IF boundary IS NULL THEN
        RAISE NOTICE 'no credit unit rescale marker: nothing to stamp, and every unstamped row is a detector candidate until one exists';
        RETURN;
    END IF;

    -- Ledger rows written well clear of the recreate window. These are the
    -- seven the issue is about: seeded by SQL, new-unit magnitudes, unstamped
    -- only because they bypassed PostEntry. Rows INSIDE the window are left
    -- alone deliberately; they stay candidates for a human.
    UPDATE public.credit_ledger_entries
       SET metadata = metadata || jsonb_build_object(
               'credit_unit', 'v2-1usd-1e9',
               'credit_unit_stamped_by', '20260902_02_credit_unit_straggler_detector.sql')
     WHERE credits_delta <> 0
       AND NOT (metadata ? 'credit_unit')
       AND created_at > boundary + interval '1 hour';
    GET DIAGNOSTICS stamped = ROW_COUNT;
    RAISE NOTICE 'stamped % post-rescale ledger entries as v2-1usd-1e9', stamped;

    -- Payment intents that 20260823_40 step 6 multiplied and never flagged.
    -- Their unit is not in doubt: that UPDATE carried no WHERE clause, so every
    -- intent that existed then was scaled. The legacy value is the same one the
    -- rescale wrote on the ledger rows it scaled, and it is the honest reading
    -- of these: rescaled from the old unit, not native to the new one.
    UPDATE public.payment_intents
       SET metadata = metadata || jsonb_build_object(
               'credit_unit', 'legacy-1usd-100k-credits',
               'credit_unit_stamped_by', '20260902_02_credit_unit_straggler_detector.sql')
     WHERE credits <> 0
       AND NOT (metadata ? 'credit_unit')
       AND created_at <= boundary;
    GET DIAGNOSTICS stamped = ROW_COUNT;
    RAISE NOTICE 'stamped % pre-rescale payment intents as legacy-1usd-100k-credits', stamped;
END
$stamp$;

COMMIT;
