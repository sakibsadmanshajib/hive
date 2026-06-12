-- =============================================================================
-- Phase 20-05: Seed tools_supported capability for known capable providers
--
-- The tools_supported column was added in 20260611_01_provider_catalog_schema.sql
-- with DEFAULT false. This migration sets it to true for openrouter and groq,
-- both of which support the OpenAI function-calling / tool-use wire format via
-- LiteLLM passthrough.
--
-- Idempotent: the UPDATE is a no-op if the rows are already true.
-- =============================================================================

begin;

UPDATE public.provider_capabilities
  SET tools_supported = true
  WHERE provider IN ('openrouter', 'groq');

commit;
