-- 20260427_01_batch_local_executor.sql
-- Phase 15: local batch executor — per-line idempotency + progress tracking.
-- Adds executor_kind column to batches plus completion progress + overconsumed
-- flag, and creates batch_lines table for restart-safe per-line state.
--
-- Filename was renumbered from 0015_* (which sorted alphabetically before the
-- 20260414_02 migration that creates public.batches) so this migration runs
-- after every prior migration in chronological order. See PR 134 review.

-- Extend batches with executor strategy + progress columns.
alter table if exists public.batches
  add column if not exists executor_kind text not null default 'upstream'
    check (executor_kind in ('upstream','local')),
  add column if not exists completed_lines integer not null default 0,
  add column if not exists failed_lines integer not null default 0,
  add column if not exists overconsumed boolean not null default false;

-- Per-line state for restart-safe resume + idempotent line settlement.
-- batch_id is text to match public.batches.id (not uuid).
create table if not exists public.batch_lines (
  batch_id text not null references public.batches(id) on delete cascade,
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
