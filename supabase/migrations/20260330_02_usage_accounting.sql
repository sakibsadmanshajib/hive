-- Phase 3: durable request-attempt and privacy-safe usage-event accounting.
-- These tables store operational and billing metadata only. Prompt, response,
-- and transcript bodies are forbidden at rest.

create table public.request_attempts (
  id                 uuid        primary key default gen_random_uuid(),
  account_id         uuid        not null references public.accounts(id) on delete cascade,
  request_id         text        not null,
  attempt_number     integer     not null check (attempt_number > 0),
  endpoint           text        not null,
  model_alias        text        not null,
  status             text        not null check (
    status in ('accepted', 'dispatching', 'streaming', 'completed', 'failed', 'cancelled', 'interrupted')
  ),
  user_id            uuid,
  team_id            uuid,
  service_account_id uuid,
  api_key_id         uuid,
  customer_tags      jsonb       not null default '{}'::jsonb,
  started_at         timestamptz not null default now(),
  completed_at       timestamptz
);

create unique index idx_request_attempts_account_request_attempt
  on public.request_attempts (account_id, request_id, attempt_number);

create index idx_request_attempts_account_started_at
  on public.request_attempts (account_id, started_at desc);

comment on table public.request_attempts is
  'Durable request-attempt facts for billing and reconciliation; raw model payloads are forbidden at rest.';

create table public.usage_events (
  id                  uuid        primary key default gen_random_uuid(),
  account_id          uuid        not null references public.accounts(id) on delete cascade,
  request_attempt_id  uuid        not null references public.request_attempts(id) on delete cascade,
  request_id          text        not null,
  event_type          text        not null check (
    event_type in ('accepted', 'reservation_created', 'stream_update', 'completed', 'released', 'refunded', 'error', 'reconciled')
  ),
  endpoint            text        not null,
  model_alias         text        not null,
  status              text        not null,
  input_tokens        bigint      not null default 0,
  output_tokens       bigint      not null default 0,
  cache_read_tokens   bigint      not null default 0,
  cache_write_tokens  bigint      not null default 0,
  hive_credit_delta   bigint      not null default 0,
  provider_request_id text,
  internal_metadata   jsonb       not null default '{}'::jsonb,
  customer_tags       jsonb       not null default '{}'::jsonb,
  error_code          text,
  error_type          text,
  created_at          timestamptz not null default now()
);

create index idx_usage_events_account_created_at
  on public.usage_events (account_id, created_at desc);

create index idx_usage_events_attempt_created_at
  on public.usage_events (request_attempt_id, created_at desc);

comment on table public.usage_events is
  'Privacy-safe usage and billing events; raw model payloads are forbidden in this table.';
