begin;

alter table public.usage_events
  add column if not exists channel text not null default 'api',
  add column if not exists api_key_id uuid references public.api_keys(id) on delete set null;

alter table public.usage_events
  drop constraint if exists usage_events_channel_check;

alter table public.usage_events
  add constraint usage_events_channel_check check (channel in ('api', 'web'));

create index if not exists usage_events_channel_created_idx
  on public.usage_events (channel, created_at desc);

create index if not exists usage_events_api_key_id_created_idx
  on public.usage_events (api_key_id, created_at desc);

commit;
