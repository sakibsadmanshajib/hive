-- API key lifecycle schema.
-- Raw API secrets are forbidden at rest. Only token hashes plus
-- customer-safe metadata (nickname, redacted suffix, timestamps) may be stored.

create table public.api_keys (
  id               uuid        primary key default gen_random_uuid(),
  account_id       uuid        not null references public.accounts(id) on delete cascade,
  nickname         text        not null,
  token_hash       text        not null unique,
  redacted_suffix  text        not null,
  status           text        not null check (status in ('active', 'disabled', 'revoked')) default 'active',
  expires_at       timestamptz,
  last_used_at     timestamptz,
  created_by_user_id uuid      not null,
  disabled_at      timestamptz,
  revoked_at       timestamptz,
  replaced_by_key_id uuid      references public.api_keys(id),
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now()
);

create index idx_api_keys_account_created_at on public.api_keys (account_id, created_at desc);

-- Audit log for key lifecycle transitions.
-- Only token hashes and customer-safe metadata may be recorded;
-- raw API secrets must never appear in event rows.
create table public.api_key_events (
  id            uuid        primary key default gen_random_uuid(),
  api_key_id    uuid        not null references public.api_keys(id) on delete cascade,
  account_id    uuid        not null references public.accounts(id) on delete cascade,
  event_type    text        not null check (event_type in ('created', 'rotated', 'disabled', 'enabled', 'revoked')),
  actor_user_id uuid        not null,
  metadata      jsonb       not null default '{}'::jsonb,
  created_at    timestamptz not null default now()
);
