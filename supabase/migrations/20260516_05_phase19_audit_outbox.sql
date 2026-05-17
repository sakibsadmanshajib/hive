-- supabase/migrations/20260516_05_phase19_audit_outbox.sql
-- Phase 19 — async fanout outbox and dead-letter queue for audit sinks.

BEGIN;

CREATE TABLE public.audit_outbox (
  id           bigserial PRIMARY KEY,
  audit_id     bigint NOT NULL,
  audit_ts     timestamptz NOT NULL,
  sink         text NOT NULL,
  attempts     int NOT NULL DEFAULT 0,
  last_error   text,
  delivered_at timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  -- The outbox always references a real audit row. The composite key
  -- matches audit_log's partitioned PRIMARY KEY (ts, id) so orphans
  -- cannot accumulate even across monthly partition boundaries.
  CONSTRAINT audit_outbox_audit_fk
    FOREIGN KEY (audit_ts, audit_id)
    REFERENCES public.audit_log(ts, id)
);

CREATE INDEX audit_outbox_undelivered
  ON public.audit_outbox(sink, created_at)
  WHERE delivered_at IS NULL;

CREATE INDEX audit_outbox_audit_id_idx
  ON public.audit_outbox(audit_id);

-- The DLQ is intentionally a snapshot: records here have already been
-- detached from the live audit_log row by the fan-out worker, so we do
-- not propagate the FK to audit_log. Define columns explicitly (rather
-- than LIKE) so the DLQ owns its own id sequence
-- (`audit_outbox_dlq_id_seq`) instead of sharing the source table's
-- sequence via copied DEFAULT expressions.
CREATE TABLE public.audit_outbox_dlq (
  id           bigserial PRIMARY KEY,
  audit_id     bigint NOT NULL,
  audit_ts     timestamptz NOT NULL,
  sink         text NOT NULL,
  attempts     int NOT NULL DEFAULT 0,
  last_error   text,
  delivered_at timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_outbox_dlq_audit_id_idx
  ON public.audit_outbox_dlq(audit_id);

GRANT INSERT, SELECT, UPDATE ON public.audit_outbox TO hive_app;
GRANT INSERT, SELECT ON public.audit_outbox_dlq TO hive_app;
-- bigserial id columns require USAGE on the backing sequence for
-- inserts that do not specify an explicit id (control-plane writer).
GRANT USAGE, SELECT ON SEQUENCE public.audit_outbox_id_seq     TO hive_app;
GRANT USAGE, SELECT ON SEQUENCE public.audit_outbox_dlq_id_seq TO hive_app;
GRANT SELECT ON public.audit_outbox     TO auditor_ro;
GRANT SELECT ON public.audit_outbox_dlq TO auditor_ro;

COMMIT;
