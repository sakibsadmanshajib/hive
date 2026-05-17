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
  created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_outbox_undelivered
  ON public.audit_outbox(sink, created_at)
  WHERE delivered_at IS NULL;

CREATE INDEX audit_outbox_audit_id_idx
  ON public.audit_outbox(audit_id);

CREATE TABLE public.audit_outbox_dlq (
  LIKE public.audit_outbox INCLUDING ALL
);

GRANT INSERT, SELECT, UPDATE ON public.audit_outbox TO hive_app;
GRANT INSERT, SELECT ON public.audit_outbox_dlq TO hive_app;
GRANT SELECT ON public.audit_outbox     TO auditor_ro;
GRANT SELECT ON public.audit_outbox_dlq TO auditor_ro;

COMMIT;
