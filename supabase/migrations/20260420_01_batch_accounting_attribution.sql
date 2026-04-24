alter table public.batches
  add column if not exists api_key_id text,
  add column if not exists model_alias text not null default '',
  add column if not exists estimated_credits bigint not null default 0,
  add column if not exists actual_credits bigint not null default 0;

create index if not exists idx_batches_api_key_id
  on public.batches(api_key_id);

create index if not exists idx_batches_model_alias
  on public.batches(model_alias);
