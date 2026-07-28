-- supabase/migrations/20260728_01_tenant_billing_account.sql
-- Backend metering, Step 1 (schema only, no behaviour change). Bridges the
-- two disjoint id spaces described in
-- apps/control-plane/internal/platform/role.go:65: public.tenant_users
-- (Phase 19, keyed by tenant_id) and public.account_memberships (Phase 2,
-- keyed by account_id) never resolve into each other today, so a session
-- principal has no account to debit. See the vault spec
-- spec-2026-07-28-backend-metering.md section 3.2 for the full rationale.

BEGIN;

-- One tenant bills to exactly one account. Single org equals single tenant
-- (D-007), and prepaid credit balance lives on the account, so this is 1:1
-- and the tenant side is the primary key.
CREATE TABLE public.tenant_billing_accounts (
  tenant_id  uuid PRIMARY KEY REFERENCES public.tenants(id)  ON DELETE CASCADE,
  account_id uuid NOT NULL    REFERENCES public.accounts(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL DEFAULT now(),
  -- An account funds at most one tenant, so a credit balance is never
  -- double-spent across two tenants.
  UNIQUE (account_id)
);

-- ON DELETE RESTRICT on account_id is deliberate: deleting an account that
-- still funds a live tenant must fail loudly rather than silently orphan a
-- tenant into unmetered service.

ALTER TABLE public.tenant_billing_accounts ENABLE ROW LEVEL SECURITY;
-- No authenticated policy and no GRANT: this mapping is control-plane-only.
-- A tenant member has no reason to read which account funds them, and the
-- front end must never see it. RLS with zero policies default-denies
-- authenticated/anon; service-role (control-plane) bypasses RLS as usual.

COMMIT;
