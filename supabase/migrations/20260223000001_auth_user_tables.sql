begin;

create table if not exists public.user_profiles (
  user_id uuid primary key references auth.users(id) on delete cascade,
  gateway_user_id text not null unique,
  email text not null unique,
  name text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists public.user_settings (
  user_id uuid not null references public.user_profiles(user_id) on delete cascade,
  setting_key text not null,
  enabled boolean not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (user_id, setting_key)
);

alter table public.user_profiles enable row level security;
alter table public.user_settings enable row level security;

drop policy if exists user_profiles_select_own on public.user_profiles;
create policy user_profiles_select_own
  on public.user_profiles
  for select
  to authenticated
  using (auth.uid() = user_id);

drop policy if exists user_profiles_update_own on public.user_profiles;
create policy user_profiles_update_own
  on public.user_profiles
  for update
  to authenticated
  using (auth.uid() = user_id)
  with check (auth.uid() = user_id);

drop policy if exists user_settings_select_own on public.user_settings;
create policy user_settings_select_own
  on public.user_settings
  for select
  to authenticated
  using (auth.uid() = user_id);

drop policy if exists user_settings_write_own on public.user_settings;
create policy user_settings_write_own
  on public.user_settings
  for all
  to authenticated
  using (auth.uid() = user_id)
  with check (auth.uid() = user_id);

drop policy if exists user_profiles_service_role_all on public.user_profiles;
create policy user_profiles_service_role_all
  on public.user_profiles
  for all
  to service_role
  using (true)
  with check (true);

drop policy if exists user_settings_service_role_all on public.user_settings;
create policy user_settings_service_role_all
  on public.user_settings
  for all
  to service_role
  using (true)
  with check (true);

commit;
