-- Phase 12 (KEY-05) — Hot-path tiered rate limiting.
--
-- Existing schema already carries per-key + per-account RPM/TPM via
-- public.api_key_rate_policies (requests_per_minute, tokens_per_minute) and
-- public.account_rate_policies. This migration extends api_key_rate_policies
-- with a tier_overrides JSONB column so an owner can override the env-driven
-- per-tier defaults at the key level. Phase 20 will wire JWT-resolved tier
-- against these overrides.
--
-- Shape of tier_overrides:
--   { "guest":      {"rpm": int, "tpm": int},
--     "unverified": {"rpm": int, "tpm": int},
--     "verified":   {"rpm": int, "tpm": int},
--     "credited":   {"rpm": int, "tpm": int} }
-- An empty object means "use env defaults". Missing tier keys also fall through
-- to env defaults — partial overrides are valid.
--
-- Range invariants enforced at the application layer (repository.UpdateLimits):
--   0 <= requests_per_minute <= 100000
--   0 <= tokens_per_minute   <= 10000000
--
-- Note: We intentionally do NOT add columns to public.api_keys. Rate-policy
-- data already lives in api_key_rate_policies and the edge-api hot path
-- already reads it via Repository.GetKeyRatePolicy.

ALTER TABLE public.api_key_rate_policies
  ADD COLUMN IF NOT EXISTS tier_overrides jsonb NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN public.api_key_rate_policies.tier_overrides IS
  'Per-tier RPM/TPM overrides keyed by tier name (guest|unverified|verified|credited). Empty object = use env defaults. Missing keys fall through to env defaults.';

-- Speed up lookup-by-key when joining hot-path resolver.
CREATE INDEX IF NOT EXISTS idx_api_key_rate_policies_updated_at
  ON public.api_key_rate_policies (updated_at);
