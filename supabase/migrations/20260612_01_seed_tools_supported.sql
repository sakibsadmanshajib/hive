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

-- provider_capabilities is keyed by route_id; the provider column lives on
-- provider_routes. Join to identify the rows belonging to openrouter and groq.
UPDATE public.provider_capabilities AS pc
  SET tools_supported = true
  FROM public.provider_routes AS pr
  WHERE pr.route_id = pc.route_id
    AND pr.provider IN ('openrouter', 'groq');

commit;
