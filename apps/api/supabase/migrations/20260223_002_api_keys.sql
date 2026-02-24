begin;

create table if not exists public.api_keys (
  key_hash text primary key,
  user_id uuid not null references public.user_profiles(user_id) on delete cascade,
  key_prefix text not null,
  scopes text[] not null default '{}',
  revoked boolean not null default false,
  created_at timestamptz not null default now(),
  revoked_at timestamptz
);

create index if not exists api_keys_user_id_idx on public.api_keys (user_id);
create index if not exists api_keys_active_user_id_idx on public.api_keys (user_id, revoked);

alter table public.api_keys enable row level security;

drop policy if exists api_keys_select_own on public.api_keys;
create policy api_keys_select_own
  on public.api_keys
  for select
  to authenticated
  using (auth.uid() = user_id);

drop policy if exists api_keys_write_own on public.api_keys;
create policy api_keys_write_own
  on public.api_keys
  for all
  to authenticated
  using (auth.uid() = user_id)
  with check (auth.uid() = user_id);

drop policy if exists api_keys_service_role_all on public.api_keys;
create policy api_keys_service_role_all
  on public.api_keys
  for all
  to service_role
  using (true)
  with check (true);

commit;
