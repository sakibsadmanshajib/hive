begin;

create table if not exists public.guest_sessions (
  guest_id text primary key,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  expires_at timestamptz not null,
  last_seen_at timestamptz,
  last_seen_ip text
);

create table if not exists public.guest_usage_events (
  id text primary key,
  guest_id text not null references public.guest_sessions(guest_id) on delete cascade,
  endpoint text not null,
  model text not null,
  credits integer not null default 0,
  ip_address text,
  created_at timestamptz not null default now()
);

create table if not exists public.guest_user_links (
  guest_id text not null references public.guest_sessions(guest_id) on delete cascade,
  user_id uuid not null references public.user_profiles(user_id) on delete cascade,
  link_source text not null,
  linked_at timestamptz not null default now(),
  primary key (guest_id, user_id)
);

create index if not exists guest_usage_events_guest_created_idx
  on public.guest_usage_events (guest_id, created_at desc);

create index if not exists guest_sessions_expires_at_idx
  on public.guest_sessions (expires_at);

create index if not exists guest_user_links_user_linked_idx
  on public.guest_user_links (user_id, linked_at desc);

alter table public.guest_sessions enable row level security;
alter table public.guest_usage_events enable row level security;
alter table public.guest_user_links enable row level security;

drop policy if exists guest_sessions_service_role_all on public.guest_sessions;
create policy guest_sessions_service_role_all
  on public.guest_sessions
  for all
  to service_role
  using (true)
  with check (true);

drop policy if exists guest_usage_events_service_role_all on public.guest_usage_events;
create policy guest_usage_events_service_role_all
  on public.guest_usage_events
  for all
  to service_role
  using (true)
  with check (true);

drop policy if exists guest_user_links_service_role_all on public.guest_user_links;
create policy guest_user_links_service_role_all
  on public.guest_user_links
  for all
  to service_role
  using (true)
  with check (true);

commit;
