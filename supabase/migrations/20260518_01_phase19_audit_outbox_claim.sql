-- supabase/migrations/20260518_01_phase19_audit_outbox_claim.sql
-- Phase 19 follow-up — exclusive row claim + retry backoff for audit_outbox.
--
-- Closes two Codex review findings on PR #143:
--   P1: SELECT ... LIMIT 50 had no FOR UPDATE SKIP LOCKED, so multi-replica
--       fanout workers raced on the same rows and produced duplicate sink
--       events. A persistent `claimed_at` lease prevents another worker
--       from re-picking a row while one is mid-flight, even across
--       restarts (the lease expires after a TTL).
--   P1: On failure the worker incremented attempts immediately and re-polled
--       at the 250ms cadence, so a transient sink outage walked rows to DLQ
--       in ~2s. A `next_retry_at` schedule honours exponential backoff.

BEGIN;

ALTER TABLE public.audit_outbox
  ADD COLUMN claimed_at    timestamptz,
  ADD COLUMN claimed_by    text,
  ADD COLUMN next_retry_at timestamptz;

-- Existing rows become immediately retryable (NULL next_retry_at <= now()).
-- Existing rows have no active claim (NULL claimed_at).

-- Partial index covers the common eligible-row scan: undelivered, retry
-- window open (next_retry_at NULL or due), and no live claim. The
-- ORDER BY next_retry_at NULLS FIRST, created_at matches the worker query
-- so it can be index-only for the LIMIT N pop.
DROP INDEX IF EXISTS audit_outbox_undelivered;
CREATE INDEX audit_outbox_eligible
  ON public.audit_outbox (next_retry_at NULLS FIRST, created_at)
  WHERE delivered_at IS NULL;

CREATE INDEX audit_outbox_claimed_at
  ON public.audit_outbox (claimed_at)
  WHERE claimed_at IS NOT NULL AND delivered_at IS NULL;

COMMIT;
