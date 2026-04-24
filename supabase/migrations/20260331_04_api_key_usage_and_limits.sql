alter table public.usage_events add column api_key_id uuid references public.api_keys(id) on delete set null;

create index idx_usage_events_api_key_created_at
  on public.usage_events (api_key_id, created_at desc);

create table public.api_key_usage_rollups (
  api_key_id         uuid        not null references public.api_keys(id) on delete cascade,
  model_alias        text        not null,
  window_kind        text        not null check (window_kind in ('lifetime', 'monthly')),
  window_start       timestamptz not null,
  request_count      bigint      not null default 0,
  input_tokens       bigint      not null default 0,
  output_tokens      bigint      not null default 0,
  cache_read_tokens  bigint      not null default 0,
  cache_write_tokens bigint      not null default 0,
  consumed_credits   bigint      not null default 0,
  last_seen_at       timestamptz not null default now(),
  primary key (api_key_id, model_alias, window_kind, window_start)
);

create table public.api_key_budget_windows (
  api_key_id       uuid        not null references public.api_keys(id) on delete cascade,
  window_kind      text        not null check (window_kind in ('lifetime', 'monthly')),
  window_start     timestamptz not null,
  window_end       timestamptz,
  consumed_credits bigint      not null default 0,
  reserved_credits bigint      not null default 0,
  updated_at       timestamptz not null default now(),
  primary key (api_key_id, window_kind, window_start)
);

create table public.api_key_rate_policies (
  api_key_id               uuid        primary key references public.api_keys(id) on delete cascade,
  requests_per_minute      integer     not null default 60,
  tokens_per_minute        integer     not null default 120000,
  rolling_five_hour_limit  bigint      not null default 0,
  weekly_limit             bigint      not null default 0,
  free_token_weight_tenths integer     not null default 1,
  updated_at               timestamptz not null default now()
);

create table public.account_rate_policies (
  account_id               uuid        primary key references public.accounts(id) on delete cascade,
  requests_per_minute      integer     not null default 60,
  tokens_per_minute        integer     not null default 120000,
  rolling_five_hour_limit  bigint      not null default 0,
  weekly_limit             bigint      not null default 0,
  free_token_weight_tenths integer     not null default 1,
  updated_at               timestamptz not null default now()
);

comment on table public.account_rate_policies is
  'Account-tier and key-tier hot-path thresholds are separate policy sources; api_key_rate_policies must not be reused for account-scope checks.';
