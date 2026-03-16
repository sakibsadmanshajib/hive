begin;

create table if not exists public.chat_sessions (
  id text primary key,
  user_id uuid references public.user_profiles(user_id) on delete cascade,
  guest_id text references public.guest_sessions(guest_id) on delete cascade,
  title text not null default 'New Chat',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  last_message_at timestamptz,
  constraint chat_sessions_owner_present check (user_id is not null or guest_id is not null)
);

create table if not exists public.chat_messages (
  id text primary key,
  session_id text not null references public.chat_sessions(id) on delete cascade,
  role text not null,
  content text not null,
  sequence_number integer not null,
  created_at timestamptz not null default now(),
  constraint chat_messages_role_valid check (role in ('system', 'user', 'assistant')),
  constraint chat_messages_sequence_positive check (sequence_number > 0),
  constraint chat_messages_session_sequence_unique unique (session_id, sequence_number)
);

create index if not exists chat_sessions_user_updated_idx
  on public.chat_sessions (user_id, updated_at desc);

create index if not exists chat_sessions_guest_updated_idx
  on public.chat_sessions (guest_id, updated_at desc);

create index if not exists chat_messages_session_sequence_idx
  on public.chat_messages (session_id, sequence_number asc);

alter table public.chat_sessions enable row level security;
alter table public.chat_messages enable row level security;

drop policy if exists chat_sessions_service_role_all on public.chat_sessions;
create policy chat_sessions_service_role_all
  on public.chat_sessions
  for all
  to service_role
  using (true)
  with check (true);

drop policy if exists chat_messages_service_role_all on public.chat_messages;
create policy chat_messages_service_role_all
  on public.chat_messages
  for all
  to service_role
  using (true)
  with check (true);

commit;
