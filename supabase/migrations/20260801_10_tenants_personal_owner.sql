-- supabase/migrations/20260801_10_tenants_personal_owner.sql
--
-- Issue #625: a self-serve signup with no unconsumed invite and no verified
-- email domain match never got a tenant, permanently. Once PR #620 made
-- API-key tenant resolution fail closed (403 account_not_provisioned), those
-- accounts stopped being merely unentitled and became unusable. Measured live
-- on 2026-07-31: eleven accounts holding active, unrevoked API keys had no
-- tenant mapping, and one of them last transacted at the exact minute the
-- credit ledger stopped recording.
--
-- The ruling on #625 is that self-serve signup provisions a PERSONAL tenant: a
-- tenant of one, not a new tenancy concept, because "one org equals one
-- tenant" (D-007) already covers a solo user as an org of one. This migration
-- adds the one piece of schema that ruling needs, and nothing else.
--
-- personal_owner_user_id marks a tenant as the personal tenant of exactly one
-- user, and the partial unique index is what makes provisioning safe under
-- concurrency. Two simultaneous provisioning calls (a Supabase Database
-- Webhook redelivery racing the console's own POST
-- /api/v1/viewer/tenant-provision, or two browser tabs) both read "this user
-- has no tenant" and both insert. A check-then-act in Go loses that race and
-- leaves a permanent duplicate tenant, each with its own billing identity and
-- its own entitlement surface. With this index the loser's INSERT ... ON
-- CONFLICT DO NOTHING matches nothing and it re-reads the winner's row
-- instead, so exactly one tenant exists no matter how many callers raced.
-- PR #624 fixed one convergence bug of exactly this shape, so the risk is
-- proven rather than theoretical.
--
-- The index is PARTIAL so it constrains only personal tenants. Every tenant
-- created any other way (the operator script, an invite-based org tenant)
-- keeps personal_owner_user_id NULL, and NULLs are unconstrained, so nothing
-- about existing tenants changes.
--
-- Billing semantics this column encodes: public.tenant_billing_accounts is
-- UNIQUE on account_id and keyed on tenant_id, so an account funds exactly one
-- tenant. A user later added to a real organization tenant therefore keeps
-- their personal tenant and their personal account's mapping unchanged, and
-- simply gains a second public.tenant_users row. Re-pointing an existing
-- mapping would retroactively re-attribute credit and usage history from the
-- personal tenant into the organization, and the ledger is append only.
--
-- Deliberately NOT here:
--   * No backfill of the existing locked-out accounts. That is done by
--     apps/control-plane/internal/signup.BackfillPersonalTenants, so it runs
--     the same conservative rule the live provisioning path runs rather than a
--     second, bespoke SQL rule that can drift from it. See #624 for what
--     happens when a mapping rule is guessed rather than proven.
--   * No CREATE EXTENSION. It is not needed here, and on a project where the
--     extension files are absent from the filesystem, CREATE EXTENSION IF NOT
--     EXISTS raises and aborts the whole transaction.
--   * No new GRANT. public.tenants is written today only by connections that
--     bypass RLS (the live control-plane DSN authenticates as a role with
--     rolbypassrls, which is also why the existing public.tenant_users writes
--     in signup work with only a SELECT grant to hive_app). Adding an INSERT
--     grant plus an RLS policy for a role that does not perform this write
--     would be speculative surface, not protection.
--
-- Safely re-runnable: every statement is IF NOT EXISTS. The live migration
-- tracker (supabase_migrations.schema_migrations) is known to be out of sync
-- with what has actually been applied, so re-runnability is a requirement here
-- rather than a nicety.
--
-- Depends on: 20260516_01_phase19_tenants.sql (public.tenants).

BEGIN;

ALTER TABLE public.tenants
  ADD COLUMN IF NOT EXISTS personal_owner_user_id uuid
  REFERENCES auth.users(id) ON DELETE SET NULL;

COMMENT ON COLUMN public.tenants.personal_owner_user_id IS
  'Non-null only on a personal (single-user) tenant created by self-serve signup, naming the user it belongs to. Unique among non-null values, so a user has at most one personal tenant. ON DELETE SET NULL rather than CASCADE: deleting an auth user must never cascade into a tenant that owns billing and usage history.';

CREATE UNIQUE INDEX IF NOT EXISTS tenants_personal_owner_user_id_key
  ON public.tenants(personal_owner_user_id)
  WHERE personal_owner_user_id IS NOT NULL;

COMMIT;
