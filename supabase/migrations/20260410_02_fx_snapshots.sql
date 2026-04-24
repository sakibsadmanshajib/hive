-- Phase 8: FX rate snapshots for BDT transactions.
create table public.fx_snapshots (
  id              uuid        primary key default gen_random_uuid(),
  account_id      uuid        not null references public.accounts(id) on delete cascade,
  base_currency   text        not null default 'USD',
  quote_currency  text        not null default 'BDT',
  mid_rate        text        not null,
  fee_rate        text        not null default '0.05',
  effective_rate  text        not null,
  source_api      text        not null check (source_api in ('xe', 'cache', 'admin_override')),
  fetched_at      timestamptz not null,
  created_at      timestamptz not null default now()
);

create index idx_fx_snapshots_account
  on public.fx_snapshots (account_id, created_at desc);
