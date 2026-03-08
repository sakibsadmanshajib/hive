begin;

create table if not exists public.credit_accounts (
  user_id uuid primary key references public.user_profiles(user_id) on delete cascade,
  available_credits integer not null default 0,
  purchased_credits integer not null default 0,
  promo_credits integer not null default 0,
  updated_at timestamptz not null default now(),
  check (available_credits >= 0),
  check (purchased_credits >= 0),
  check (promo_credits >= 0)
);

create table if not exists public.credit_ledger (
  id bigserial primary key,
  user_id uuid not null references public.user_profiles(user_id) on delete cascade,
  entry_type text not null check (entry_type in ('credit', 'debit')),
  credits integer not null check (credits > 0),
  reference_type text not null,
  reference_id text not null,
  created_at timestamptz not null default now(),
  unique (reference_type, reference_id)
);

create table if not exists public.usage_events (
  id text primary key,
  user_id uuid not null references public.user_profiles(user_id) on delete cascade,
  endpoint text not null,
  model text not null,
  credits integer not null check (credits >= 0),
  created_at timestamptz not null default now()
);

create table if not exists public.payment_intents (
  intent_id text primary key,
  user_id uuid not null references public.user_profiles(user_id) on delete cascade,
  provider text not null,
  bdt_amount numeric(12,2) not null check (bdt_amount >= 0),
  status text not null check (status in ('initiated', 'credited', 'failed')),
  minted_credits integer not null default 0,
  created_at timestamptz not null default now(),
  check (minted_credits >= 0)
);

create table if not exists public.payment_events (
  id bigserial primary key,
  event_key text unique not null,
  intent_id text not null references public.payment_intents(intent_id) on delete cascade,
  provider text not null,
  provider_txn_id text not null,
  verified boolean not null,
  created_at timestamptz not null default now()
);

create index if not exists credit_ledger_user_id_created_idx
  on public.credit_ledger (user_id, created_at desc);
create index if not exists usage_events_user_id_created_idx
  on public.usage_events (user_id, created_at desc);
create index if not exists payment_intents_user_id_created_idx
  on public.payment_intents (user_id, created_at desc);
create index if not exists payment_events_intent_id_idx
  on public.payment_events (intent_id);

alter table public.credit_accounts enable row level security;
alter table public.credit_ledger enable row level security;
alter table public.usage_events enable row level security;
alter table public.payment_intents enable row level security;
alter table public.payment_events enable row level security;

drop policy if exists credit_accounts_select_own on public.credit_accounts;
create policy credit_accounts_select_own
  on public.credit_accounts
  for select
  to authenticated
  using (auth.uid() = user_id);

drop policy if exists credit_ledger_select_own on public.credit_ledger;
create policy credit_ledger_select_own
  on public.credit_ledger
  for select
  to authenticated
  using (auth.uid() = user_id);

drop policy if exists usage_events_select_own on public.usage_events;
create policy usage_events_select_own
  on public.usage_events
  for select
  to authenticated
  using (auth.uid() = user_id);

drop policy if exists payment_intents_select_own on public.payment_intents;
create policy payment_intents_select_own
  on public.payment_intents
  for select
  to authenticated
  using (auth.uid() = user_id);

drop policy if exists payment_events_select_own on public.payment_events;
create policy payment_events_select_own
  on public.payment_events
  for select
  to authenticated
  using (
    exists (
      select 1
      from public.payment_intents i
      where i.intent_id = payment_events.intent_id
        and i.user_id = auth.uid()
    )
  );

drop policy if exists credit_accounts_service_role_all on public.credit_accounts;
create policy credit_accounts_service_role_all
  on public.credit_accounts
  for all
  to service_role
  using (true)
  with check (true);

drop policy if exists credit_ledger_service_role_all on public.credit_ledger;
create policy credit_ledger_service_role_all
  on public.credit_ledger
  for all
  to service_role
  using (true)
  with check (true);

drop policy if exists usage_events_service_role_all on public.usage_events;
create policy usage_events_service_role_all
  on public.usage_events
  for all
  to service_role
  using (true)
  with check (true);

drop policy if exists payment_intents_service_role_all on public.payment_intents;
create policy payment_intents_service_role_all
  on public.payment_intents
  for all
  to service_role
  using (true)
  with check (true);

drop policy if exists payment_events_service_role_all on public.payment_events;
create policy payment_events_service_role_all
  on public.payment_events
  for all
  to service_role
  using (true)
  with check (true);

commit;
