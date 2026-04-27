-- Phase 14 — Payments / Budget / Grant
-- Creates: budgets, spend_alerts, invoices, credit_grants
-- Adds: accounts.is_platform_admin column (Phase 14 RBAC contract stub)
-- Trigger: credit_grants append-only (BEFORE UPDATE OR DELETE)
--
-- Schema notes:
--   * Workspace = public.accounts (the v1.0 tenancy entity). The Phase 14
--     audit (Section B) referenced public.workspaces; reconciled to
--     public.accounts to match the actual identity foundation migration
--     (20260328_01_identity_foundation.sql). Recorded as Rule 1 deviation.
--   * All BDT money columns use bigint (subunit precision = paisa, 1 BDT = 100
--     paisa). math/big is enforced application-side; bigint guarantees exact
--     subunit storage with no decimal ambiguity.
--   * Forward-only migration (project policy — no down migration).

-- ============================================================================
-- accounts.is_platform_admin — platform-admin column (defaults false)
-- Phase 18 will refine RBAC; Phase 14 ships the column + flag plumbing.
-- ============================================================================
ALTER TABLE public.accounts
    ADD COLUMN IF NOT EXISTS is_platform_admin boolean NOT NULL DEFAULT false;

-- ============================================================================
-- budgets — per-workspace soft + hard caps in BDT subunits
-- ============================================================================
CREATE TABLE IF NOT EXISTS public.budgets (
    workspace_id              uuid PRIMARY KEY REFERENCES public.accounts(id) ON DELETE CASCADE,
    period_start              date NOT NULL,
    soft_cap_bdt_subunits     bigint NOT NULL CHECK (soft_cap_bdt_subunits >= 0),
    hard_cap_bdt_subunits     bigint NOT NULL CHECK (hard_cap_bdt_subunits >= soft_cap_bdt_subunits),
    currency                  char(3) NOT NULL DEFAULT 'BDT' CHECK (currency = 'BDT'),
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_budgets_workspace_period
    ON public.budgets (workspace_id, period_start);

-- ============================================================================
-- spend_alerts — owner-managed thresholds + delivery channels
-- ============================================================================
CREATE TABLE IF NOT EXISTS public.spend_alerts (
    id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id              uuid NOT NULL REFERENCES public.accounts(id) ON DELETE CASCADE,
    threshold_pct             smallint NOT NULL CHECK (threshold_pct IN (50, 80, 100)),
    email                     text,
    webhook_url               text,
    webhook_secret            text,
    last_fired_at             timestamptz,
    last_fired_period         date,
    created_at                timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, threshold_pct)
);

CREATE INDEX IF NOT EXISTS idx_spend_alerts_workspace
    ON public.spend_alerts (workspace_id);

-- ============================================================================
-- invoices — monthly BDT-only invoice rows
-- Distinct from payment_invoices (20260411_01) and from payments.Invoice
-- checkout struct that Phase 17 owns; this is the NEW Phase 14 invoice surface.
-- ============================================================================
CREATE TABLE IF NOT EXISTS public.invoices (
    id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id              uuid NOT NULL REFERENCES public.accounts(id) ON DELETE CASCADE,
    period_start              date NOT NULL,
    period_end                date NOT NULL,
    total_bdt_subunits        bigint NOT NULL CHECK (total_bdt_subunits >= 0),
    line_items                jsonb NOT NULL,
    pdf_storage_key           text,
    currency                  char(3) NOT NULL DEFAULT 'BDT' CHECK (currency = 'BDT'),
    generated_at              timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, period_start)
);

CREATE INDEX IF NOT EXISTS idx_invoices_workspace_period
    ON public.invoices (workspace_id, period_start);

-- ============================================================================
-- credit_grants — owner-discretionary credit issuance, append-only at schema
-- DELIBERATELY NO updated_at column. Trigger blocks UPDATE/DELETE.
-- ============================================================================
CREATE TABLE IF NOT EXISTS public.credit_grants (
    id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    granted_by_user_id        uuid NOT NULL REFERENCES auth.users(id),
    granted_to_user_id        uuid NOT NULL REFERENCES auth.users(id),
    granted_to_workspace_id   uuid NOT NULL REFERENCES public.accounts(id),
    amount_bdt_subunits       bigint NOT NULL CHECK (amount_bdt_subunits > 0),
    reason_note               text,
    ledger_entry_id           uuid NOT NULL,
    currency                  char(3) NOT NULL DEFAULT 'BDT' CHECK (currency = 'BDT'),
    created_at                timestamptz NOT NULL DEFAULT now()
    -- DELIBERATELY NO updated_at — schema-level immutability
);

CREATE INDEX IF NOT EXISTS idx_credit_grants_grantee
    ON public.credit_grants (granted_to_workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_credit_grants_grantor
    ON public.credit_grants (granted_by_user_id, created_at DESC);

-- ----------------------------------------------------------------------------
-- credit_grants_immutable_trg — schema-level append-only enforcement
-- BEFORE UPDATE OR DELETE — RAISE EXCEPTION 'append-only ledger violation'
-- ----------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION public.tg_credit_grants_immutable()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'credit_grants is append-only — UPDATE/DELETE forbidden (append-only ledger violation)'
        USING ERRCODE = 'integrity_constraint_violation';
END;
$$;

DROP TRIGGER IF EXISTS credit_grants_immutable_trg ON public.credit_grants;
CREATE TRIGGER credit_grants_immutable_trg
    BEFORE UPDATE OR DELETE ON public.credit_grants
    FOR EACH ROW EXECUTE FUNCTION public.tg_credit_grants_immutable();
