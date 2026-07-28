-- supabase/migrations/20260728_04_metering_shadow_verdicts.sql
-- Metering Step 2 (shadow mode). Log-only: this table is never read by any
-- enforcement path and debits nothing. It exists to grade the precedence
-- order (apps/edge-api/internal/metering/precedence.go) and the per-model
-- pricing formula against real traffic before Step 4 turns either into a
-- refusal or a debit. See vault spec-2026-07-28-backend-metering.md
-- section 9.2 Step 2, and decision-2026-07-28-metering-rulings.md.
--
-- Purely additive: no existing table is altered. This table can be dropped
-- without consequence if shadow mode is abandoned -- Step 2 is reversible
-- by construction (see .wolf/decisions.md D-027). Safe to apply against a
-- live database while the current code runs: nothing reads or writes this
-- table until a later PR wires apps/edge-api/internal/metering.Gate into a
-- dispatch path (this PR ships the package wired to nothing).

BEGIN;

CREATE TABLE public.metering_shadow_verdicts (
  id                          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  request_id                  text NOT NULL,
  tenant_id                   uuid REFERENCES public.tenants(id) ON DELETE SET NULL,
  account_id                  uuid REFERENCES public.accounts(id) ON DELETE SET NULL,
  principal_type              text NOT NULL CHECK (principal_type IN ('api_key','session')),
  deployment                  text CHECK (deployment IN ('HIVE_CLOUD','ENTERPRISE_EDGE')),
  endpoint                    text NOT NULL,
  model_alias                 text NOT NULL,
  precedence_rule             text NOT NULL,
  verdict                     text NOT NULL CHECK (verdict IN ('billable','not_billable')),
  would_refuse_code           text, -- null, or insufficient_quota / billing_not_configured / billing_unavailable
  prompt_tokens               bigint NOT NULL DEFAULT 0,
  completion_tokens           bigint NOT NULL DEFAULT 0,
  terminal_usage_confirmed    boolean NOT NULL DEFAULT false,
  estimated_credits_legacy    bigint NOT NULL DEFAULT 0, -- today's int64(total_tokens) convention
  estimated_credits_per_model bigint NOT NULL DEFAULT 0, -- provisional, unit TBD (spec section 12 item 1)
  disconnected                boolean NOT NULL DEFAULT false,
  delivered_tokens            bigint, -- accumulated-at-disconnect, null unless disconnected
  created_at                  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_metering_shadow_verdicts_tenant_created
  ON public.metering_shadow_verdicts (tenant_id, created_at DESC);
CREATE INDEX idx_metering_shadow_verdicts_verdict_created
  ON public.metering_shadow_verdicts (verdict, created_at DESC);

ALTER TABLE public.metering_shadow_verdicts ENABLE ROW LEVEL SECURITY;
-- Same posture as tenant_billing_accounts (20260728_01): no authenticated
-- policy, no GRANT. This table can reveal account_id/tenant billing mapping
-- and a would-be-refusal reason, so it stays control-plane/edge-api
-- service-role only. A tenant member has no product reason to read it.

COMMIT;
