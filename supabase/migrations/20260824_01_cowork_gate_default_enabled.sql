-- Issue #1107: Cowork is unlaunchable on every workspace that has no explicit
-- ENABLE_COWORK tenant_settings row. The gate registry had no concept of a
-- default state, so "unset" read as false everywhere: only tenants hand-seeded
-- by scripts/seed-demo-owner.py could launch agent tasks, which is why the
-- demo workspace worked while the parity-audit user's workspace answered 403
-- ("The agent service is not enabled for this organization").
--
-- This is the defaults layer the 20260715_04_featuregate_dynamic_keys.sql
-- header anticipated: a per-key default carried ON the registry itself, so
-- the resolution rule becomes "explicit tenant_settings row wins; otherwise
-- the key's declared default". No per-tenant backfill is needed: existing and
-- future workspaces both pick the default up at read time.
--
-- Scope is deliberately one key: ENABLE_COWORK only. RAG, voice, relay, SSO,
-- billing, and audit keys keep their current opt-in behavior because their
-- default stays false, and an admin can still turn Cowork off per workspace
-- by writing an enabled=false tenant_settings row through the existing
-- feature-gate admin surface, which overrides any default.

ALTER TABLE public.feature_gate_keys
  ADD COLUMN IF NOT EXISTS default_enabled boolean NOT NULL DEFAULT false;

UPDATE public.feature_gate_keys
   SET default_enabled = true
 WHERE key = 'ENABLE_COWORK';

COMMENT ON COLUMN public.feature_gate_keys.default_enabled IS
  'State reported for this gate when the tenant has no explicit tenant_settings row. Explicit rows always win over the declared default.';
