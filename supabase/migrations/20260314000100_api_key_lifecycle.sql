begin;

create extension if not exists pgcrypto;

alter table public.api_keys
  add column if not exists id uuid default gen_random_uuid(),
  add column if not exists nickname text,
  add column if not exists expires_at timestamptz;

update public.api_keys
set
  id = coalesce(id, gen_random_uuid()),
  nickname = coalesce(nickname, key_prefix)
where id is null or nickname is null;

alter table public.api_keys
  alter column id set not null,
  alter column nickname set not null;

create unique index if not exists api_keys_id_idx on public.api_keys (id);
create index if not exists api_keys_expires_at_idx on public.api_keys (expires_at);

create table if not exists public.api_key_events (
  id uuid primary key default gen_random_uuid(),
  api_key_id uuid not null references public.api_keys(id) on delete cascade,
  user_id uuid not null references public.user_profiles(user_id) on delete cascade,
  event_type text not null,
  metadata jsonb not null default '{}'::jsonb,
  event_at timestamptz not null default now(),
  constraint api_key_events_event_type_check check (event_type in ('created', 'revoked', 'expired_observed'))
);

create index if not exists api_key_events_api_key_id_idx on public.api_key_events (api_key_id, event_at desc);
create index if not exists api_key_events_user_id_idx on public.api_key_events (user_id, event_at desc);

alter table public.api_key_events enable row level security;

drop policy if exists api_key_events_select_own on public.api_key_events;
create policy api_key_events_select_own
  on public.api_key_events
  for select
  to authenticated
  using (auth.uid() = user_id);

drop policy if exists api_key_events_service_role_all on public.api_key_events;
create policy api_key_events_service_role_all
  on public.api_key_events
  for all
  to service_role
  using (true)
  with check (true);

commit;
