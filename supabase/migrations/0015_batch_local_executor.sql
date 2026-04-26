-- 0015_batch_local_executor.sql
-- Phase 15: local batch executor — per-line idempotency + progress tracking.
-- Adds executor_kind column to batches plus completion progress + overconsumed
-- flag, and creates batch_lines table for restart-safe per-line state.

-- Extend batches with executor strategy + progress columns.
alter table public.batches
  add column if not exists executor_kind text not null default 'upstream'
    check (executor_kind in ('upstream','local')),
  add column if not exists completed_lines integer not null default 0,
  add column if not exists failed_lines integer not null default 0,
  add column if not exists overconsumed boolean not null default false;

-- Tag the route_capabilities table with the executor strategy used by the
-- submitter to choose between local executor and LiteLLM upstream batch upload.
-- Default is 'local' since the v1.0 + v1.1 provider mix (openrouter + groq)
-- has no LiteLLM-supported batch upstream.
alter table if exists public.route_capabilities
  add column if not exists executor_kind text not null default 'local'
    check (executor_kind in ('upstream','local'));

-- Per-line state for restart-safe resume + idempotent line settlement.
create table if not exists public.batch_lines (
  batch_id uuid not null references public.batches(id) on delete cascade,
  custom_id text not null,
  status text not null check (status in ('pending','in_progress','succeeded','failed')),
  attempt integer not null default 0,
  consumed_credits numeric(20,6) not null default 0,
  output_index integer,
  error_index integer,
  last_error text,
  first_seen_at timestamptz not null default now(),
  completed_at timestamptz,
  primary key (batch_id, custom_id)
);

create index if not exists batch_lines_batch_status_idx
  on public.batch_lines (batch_id, status);
