-- Phase 8: payment intent state machine and event log.
create table public.payment_intents (
  id                uuid        primary key default gen_random_uuid(),
  account_id        uuid        not null references public.accounts(id) on delete cascade,
  rail              text        not null check (rail in ('stripe', 'bkash', 'sslcommerz')),
  status            text        not null default 'created' check (
    status in ('created','pending_redirect','provider_processing','confirming','completed','failed','expired','cancelled')
  ),
  credits           bigint      not null,
  amount_usd        bigint      not null,
  amount_local      bigint      not null default 0,
  local_currency    text        not null default '',
  fx_snapshot_id    uuid,
  provider_intent_id text       not null default '',
  redirect_url      text        not null default '',
  tax_treatment     text        not null default 'no_tax',
  tax_rate          text        not null default '0.00',
  tax_amount_local  bigint      not null default 0,
  idempotency_key   text        not null,
  confirming_at     timestamptz,
  expires_at        timestamptz,
  metadata          jsonb       not null default '{}'::jsonb,
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now()
);

create unique index idx_payment_intents_account_idempotency
  on public.payment_intents (account_id, idempotency_key);
create index idx_payment_intents_account_status
  on public.payment_intents (account_id, status);
create index idx_payment_intents_confirming
  on public.payment_intents (status, confirming_at)
  where status = 'confirming';

create table public.payment_events (
  id                 uuid        primary key default gen_random_uuid(),
  payment_intent_id  uuid        not null references public.payment_intents(id) on delete cascade,
  event_type         text        not null,
  rail               text        not null,
  provider_event_id  text        not null default '',
  raw_payload        jsonb       not null default '{}'::jsonb,
  created_at         timestamptz not null default now()
);

create index idx_payment_events_intent
  on public.payment_events (payment_intent_id, created_at desc);
