-- supabase/migrations/20260516_04_phase19_audit_log.sql
-- Phase 19 — append-only audit log, partitioned monthly, hash-chained.

BEGIN;

-- Application role used by control-plane and edge-api.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'hive_app') THEN
    CREATE ROLE hive_app NOLOGIN;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'auditor_ro') THEN
    CREATE ROLE auditor_ro NOLOGIN;
  END IF;
END
$$;

CREATE TABLE public.audit_log (
  id                bigserial,
  tenant_id         uuid,
  actor_id          uuid,
  actor_type        text NOT NULL CHECK (actor_type IN ('USER','SERVICE','SYSTEM','EXTERNAL')),
  action            text NOT NULL,
  resource_type     text,
  resource_id       text,
  severity          text NOT NULL CHECK (severity IN ('DEBUG','INFO','NOTICE','WARNING','ERROR','CRITICAL')),
  before_json       jsonb,
  after_json        jsonb,
  request_id        uuid,
  source_ip         inet,
  user_agent        text,
  jwt_claims_digest text,
  deploy_sha        text NOT NULL,
  env               text NOT NULL,
  ts                timestamptz NOT NULL DEFAULT clock_timestamp(),
  seq               bigint NOT NULL,
  prev_hash         bytea NOT NULL,
  row_hash          bytea NOT NULL,
  -- The tamper-evident chain is SHA-256 so the digests must be exactly
  -- 32 bytes. A length CHECK at the table catches malformed writes
  -- before they enter the chain.
  CONSTRAINT audit_log_prev_hash_len CHECK (octet_length(prev_hash) = 32),
  CONSTRAINT audit_log_row_hash_len  CHECK (octet_length(row_hash) = 32),
  PRIMARY KEY (ts, id)
) PARTITION BY RANGE (ts);

CREATE INDEX audit_log_tenant_ts_idx ON public.audit_log (tenant_id, ts DESC);
CREATE INDEX audit_log_action_ts_idx ON public.audit_log (action, ts DESC);
CREATE INDEX audit_log_severity_ts_idx ON public.audit_log (severity, ts DESC)
  WHERE severity IN ('ERROR','CRITICAL');

-- Current month + next month partitions. The control-plane will create future
-- partitions on a daily cron in a later plan; bootstrap two months here.
CREATE TABLE public.audit_log_2026_05 PARTITION OF public.audit_log
  FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE public.audit_log_2026_06 PARTITION OF public.audit_log
  FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

-- Per-partition seq must be monotonic. The Go helper enforces this by
-- selecting MAX(seq) for the partition under SERIALIZABLE; the UNIQUE
-- constraint here adds defense-in-depth so manual inserts or future
-- application bugs cannot create duplicate seq values that would break
-- the hash chain.
CREATE UNIQUE INDEX audit_log_2026_05_seq_uidx ON public.audit_log_2026_05 (seq);
CREATE UNIQUE INDEX audit_log_2026_06_seq_uidx ON public.audit_log_2026_06 (seq);

REVOKE ALL ON public.audit_log FROM PUBLIC;
GRANT INSERT, SELECT ON public.audit_log TO hive_app;
-- `id` is bigserial; nextval() requires USAGE on the backing sequence in
-- addition to INSERT on the table, otherwise hive_app inserts fail with
-- "permission denied for sequence audit_log_id_seq".
GRANT USAGE, SELECT ON SEQUENCE public.audit_log_id_seq TO hive_app;
GRANT SELECT ON public.audit_log TO auditor_ro;
GRANT USAGE ON SCHEMA public TO auditor_ro;

-- Cold archive manifest used by the daily archive job.
CREATE TABLE public.audit_cold_archive_manifest (
  partition_name text PRIMARY KEY,
  archived_at    timestamptz NOT NULL,
  parquet_path   text NOT NULL,
  parquet_sha256 text NOT NULL,
  row_count      bigint NOT NULL,
  last_prev_hash bytea NOT NULL,
  last_row_hash  bytea NOT NULL
);

COMMIT;
