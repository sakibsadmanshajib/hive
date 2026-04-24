-- Phase 3: immutable workspace credit ledger and idempotency foundation.
-- These tables store financial and operational metadata only. They must never
-- store prompts, completions, or other request/response bodies.

create table public.credit_ledger_entries (
  id               uuid        primary key default gen_random_uuid(),
  account_id       uuid        not null references public.accounts(id) on delete cascade,
  entry_type       text        not null check (
    entry_type in (
      'grant',
      'adjustment',
      'reservation_hold',
      'reservation_release',
      'usage_charge',
      'refund'
    )
  ),
  credits_delta    bigint      not null,
  idempotency_key  text        not null,
  request_id       text,
  attempt_id       uuid,
  reservation_id   uuid,
  metadata         jsonb       not null default '{}'::jsonb,
  created_at       timestamptz not null default now()
);

create unique index idx_credit_ledger_entries_account_idempotency
  on public.credit_ledger_entries (account_id, entry_type, idempotency_key);

create index idx_credit_ledger_entries_account_created_at
  on public.credit_ledger_entries (account_id, created_at desc);

comment on table public.credit_ledger_entries is
  'Immutable credit ledger entries with financial metadata only; never store prompt or response bodies.';

create table public.credit_idempotency_keys (
  account_id        uuid        not null references public.accounts(id) on delete cascade,
  operation_type    text        not null,
  idempotency_key   text        not null,
  ledger_entry_id   uuid        references public.credit_ledger_entries(id),
  request_id        text,
  attempt_id        uuid,
  created_at        timestamptz not null default now(),
  primary key (account_id, operation_type, idempotency_key)
);

comment on table public.credit_idempotency_keys is
  'Idempotency tracking for financial mutations with metadata only; never store prompt or response bodies.';
