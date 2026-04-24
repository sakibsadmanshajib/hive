-- Per-key policy configuration for model access, budgets, and governance.

create table public.api_key_policies (
  api_key_id         uuid        primary key references public.api_keys(id) on delete cascade,
  allow_all_models   boolean     not null default false,
  allowed_group_names jsonb      not null default '["default"]'::jsonb,
  allowed_aliases    jsonb       not null default '[]'::jsonb,
  denied_aliases     jsonb       not null default '[]'::jsonb,
  budget_kind        text        not null check (budget_kind in ('none', 'lifetime', 'monthly')) default 'none',
  budget_limit_credits bigint,
  budget_anchor_at   timestamptz,
  policy_version     bigint      not null default 1,
  updated_at         timestamptz not null default now()
);

-- Reusable model groups for key access governance.
create table public.model_policy_groups (
  name               text        primary key,
  description        text        not null,
  auto_attach_new_aliases boolean not null default false
);

insert into public.model_policy_groups (name, description, auto_attach_new_aliases) values
  ('default', 'Default launch-safe models', false),
  ('premium', 'Premium or higher-cost models', false),
  ('oss',     'Open-source public aliases', false),
  ('closed',  'Closed or hosted provider-backed aliases', true);

-- Membership linking groups to model aliases.
create table public.model_policy_group_members (
  group_name text not null references public.model_policy_groups(name) on delete cascade,
  alias_id   text not null references public.model_aliases(alias_id) on delete cascade,
  primary key (group_name, alias_id)
);

insert into public.model_policy_group_members (group_name, alias_id) values
  ('default', 'hive-default'),
  ('default', 'hive-fast'),
  ('premium', 'hive-auto'),
  ('closed',  'hive-default'),
  ('closed',  'hive-fast'),
  ('closed',  'hive-auto');
