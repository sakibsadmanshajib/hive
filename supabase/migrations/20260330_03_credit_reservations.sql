-- Phase 3: durable credit reservations and reconciliation jobs.
-- These tables store financial and operational metadata only. Prompt,
-- completion, and transcript bodies are forbidden at rest.

create table public.credit_reservations (
  id                       uuid        primary key default gen_random_uuid(),
  account_id               uuid        not null references public.accounts(id) on delete cascade,
  request_attempt_id       uuid        not null references public.request_attempts(id) on delete cascade,
  reservation_key          text        not null unique,
  policy_mode              text        not null check (policy_mode in ('strict', 'temporary_overage')) default 'strict',
  status                   text        not null check (status in ('active', 'expanded', 'finalized', 'released', 'needs_reconciliation')),
  reserved_credits         bigint      not null check (reserved_credits > 0),
  consumed_credits         bigint      not null default 0,
  released_credits         bigint      not null default 0,
  terminal_usage_confirmed boolean     not null default false,
  created_at               timestamptz not null default now(),
  updated_at               timestamptz not null default now()
);

create index idx_credit_reservations_account_updated_at
  on public.credit_reservations (account_id, updated_at desc);

comment on table public.credit_reservations is
  'Durable reservation state for request attempts; raw model payloads are forbidden at rest.';

create table public.credit_reservation_events (
  id               uuid        primary key default gen_random_uuid(),
  reservation_id   uuid        not null references public.credit_reservations(id) on delete cascade,
  event_type       text        not null check (event_type in ('reserved', 'expanded', 'finalized', 'released', 'refunded', 'marked_for_reconciliation')),
  credits_delta    bigint      not null,
  reason           text        not null,
  metadata         jsonb       not null default '{}'::jsonb,
  created_at       timestamptz not null default now()
);

create index idx_credit_reservation_events_reservation_created_at
  on public.credit_reservation_events (reservation_id, created_at desc);

comment on table public.credit_reservation_events is
  'Immutable reservation lifecycle events with financial metadata only; prompt and response bodies are forbidden.';

create table public.credit_reconciliation_jobs (
  id                 uuid        primary key default gen_random_uuid(),
  reservation_id     uuid        not null references public.credit_reservations(id) on delete cascade,
  request_attempt_id uuid        not null references public.request_attempts(id) on delete cascade,
  reason             text        not null,
  status             text        not null check (status in ('pending', 'processing', 'resolved')) default 'pending',
  run_after          timestamptz not null default now(),
  created_at         timestamptz not null default now()
);

create index idx_credit_reconciliation_jobs_status_run_after
  on public.credit_reconciliation_jobs (status, run_after asc);

comment on table public.credit_reconciliation_jobs is
  'Reconciliation backlog for ambiguous or late-arriving reservation settlement facts.';
