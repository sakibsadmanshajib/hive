-- =============================================================================
-- #628 — durable record (dead letter) for every inbound payment webhook
-- =============================================================================
-- Settlement used to persist nothing until the intent had been resolved, so a
-- failure while parsing the payload or looking up the intent left no trace that
-- the delivery ever arrived. Combined with a 200 response the loss was both
-- unrecoverable and invisible: money collected, credits never granted.
--
-- One row per inbound delivery attempt (retries are separate rows on purpose —
-- the retry history is the operational signal). `status <> 'processed'` is the
-- dead-letter queue.
--
-- `raw_body` is text, not jsonb: a dead letter must accept a payload that is not
-- valid JSON, which is exactly the case a jsonb column would reject and thereby
-- lose. `payment_intent_id` is nullable because a delivery that fails before the
-- intent is resolved still has to be recorded.
--
-- Safely re-runnable: every object is created IF NOT EXISTS and the policy is
-- dropped before it is recreated.
-- =============================================================================

begin;

create table if not exists public.payment_webhook_deliveries (
  id                uuid        primary key,
  rail              text        not null,
  payment_intent_id uuid        references public.payment_intents(id) on delete set null,
  status            text        not null default 'received' check (status in ('received', 'processed', 'failed')),
  event_type        text        not null default '',
  error_detail      text        not null default '',
  raw_body          text        not null default '',
  received_at       timestamptz not null default now(),
  updated_at        timestamptz not null default now()
);

comment on table public.payment_webhook_deliveries is
  'Inbound payment webhook deliveries; rows with status <> ''processed'' are the settlement dead letter. Financial metadata and provider payloads only.';

-- The dead-letter query: unsettled deliveries, newest first.
create index if not exists idx_payment_webhook_deliveries_unsettled
  on public.payment_webhook_deliveries (received_at desc)
  where status <> 'processed';

create index if not exists idx_payment_webhook_deliveries_intent
  on public.payment_webhook_deliveries (payment_intent_id, received_at desc);

-- RLS mirrors 20260529_01 for the payment_* family: control-plane (hive_app)
-- full access, no policy for anon/authenticated so a published key reads no
-- rows. Guarded on the role existing so the migration also applies to a plain
-- Postgres without the Supabase-managed roles.
do $$
begin
  execute 'alter table public.payment_webhook_deliveries enable row level security';
  execute 'alter table public.payment_webhook_deliveries force row level security';
  if exists (select 1 from pg_roles where rolname = 'hive_app') then
    execute 'drop policy if exists payment_webhook_deliveries_service_role_all on public.payment_webhook_deliveries';
    execute 'create policy payment_webhook_deliveries_service_role_all on public.payment_webhook_deliveries
               for all to hive_app using (true) with check (true)';
  end if;
end $$;

commit;
