-- Phase 2: Identity & Account Foundation
-- Creates the workspace tenancy schema: accounts, memberships, invitations, and core profiles.

-- ============================================================
-- accounts
-- The top-level workspace entity. Every Hive user owns at least
-- one account (personal or business) upon sign-up.
-- ============================================================
create table public.accounts (
  id             uuid        primary key default gen_random_uuid(),
  slug           text        not null unique,
  display_name   text        not null,
  account_type   text        not null check (account_type in ('personal', 'business')),
  owner_user_id  uuid        not null references auth.users(id),
  created_at     timestamptz not null default now(),
  updated_at     timestamptz not null default now()
);

create index idx_accounts_slug on public.accounts (slug);

-- ============================================================
-- account_memberships
-- Links Supabase auth users to accounts with a role and status.
-- Unique constraint prevents duplicate memberships.
-- ============================================================
create table public.account_memberships (
  id          uuid        primary key default gen_random_uuid(),
  account_id  uuid        not null references public.accounts(id) on delete cascade,
  user_id     uuid        not null references auth.users(id) on delete cascade,
  role        text        not null check (role in ('owner', 'member')),
  status      text        not null check (status in ('active', 'invited')),
  created_at  timestamptz not null default now(),
  unique (account_id, user_id)
);

create index idx_account_memberships_user_id on public.account_memberships (user_id);

-- ============================================================
-- account_invitations
-- Tracks pending email invitations to join an account.
-- token_hash is stored instead of the raw token for security.
-- ============================================================
create table public.account_invitations (
  id                  uuid        primary key default gen_random_uuid(),
  account_id          uuid        not null references public.accounts(id) on delete cascade,
  email               text        not null,
  role                text        not null check (role in ('member')),
  token_hash          text        not null unique,
  expires_at          timestamptz not null,
  accepted_at         timestamptz,
  invited_by_user_id  uuid        not null references auth.users(id),
  created_at          timestamptz not null default now()
);

create index idx_account_invitations_email on public.account_invitations (email);

-- ============================================================
-- account_profiles
-- Core pre-billing profile data collected during onboarding.
-- Billing and tax details are collected separately when needed.
-- ============================================================
create table public.account_profiles (
  account_id             uuid    primary key references public.accounts(id) on delete cascade,
  owner_name             text    not null,
  login_email            text    not null,
  country_code           text,
  state_region           text,
  profile_setup_complete boolean not null default false,
  created_at             timestamptz not null default now(),
  updated_at             timestamptz not null default now()
);
