-- supabase/migrations/20260516_06_phase19_llm_traces.sql
-- Phase 19 — high-cardinality LLM-call detail, partitioned monthly.

BEGIN;

CREATE TABLE public.llm_traces (
  id                uuid NOT NULL DEFAULT gen_random_uuid(),
  tenant_id         uuid NOT NULL,
  user_id           uuid,
  request_id        uuid NOT NULL,
  model             text NOT NULL,
  provider          text NOT NULL,
  in_tokens         int  NOT NULL,
  out_tokens        int  NOT NULL,
  latency_ms        int  NOT NULL,
  cost_credits      bigint NOT NULL,
  finish_reason     text,
  prompt_hash       text NOT NULL,
  completion_hash   text NOT NULL,
  retrieval_doc_ids text[],
  langfuse_trace_id text,
  ts                timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (ts, id)
) PARTITION BY RANGE (ts);

CREATE INDEX llm_traces_tenant_ts_idx ON public.llm_traces (tenant_id, ts DESC);
CREATE INDEX llm_traces_request_idx   ON public.llm_traces (request_id);

CREATE TABLE public.llm_traces_2026_05 PARTITION OF public.llm_traces
  FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE public.llm_traces_2026_06 PARTITION OF public.llm_traces
  FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

GRANT INSERT, SELECT ON public.llm_traces TO hive_app;
GRANT SELECT ON public.llm_traces TO auditor_ro;

COMMIT;
