-- supabase/migrations/20260518_02_phase19_embedding_openrouter_primary.sql
-- NVIDIA retired its retrieval NIM models in bulk on 2026-05-18 (both
-- `nvidia/llama-3.2-nemoretriever-300m-embed-v1` and
-- `nvidia/llama-3.2-nv-embedqa-1b-v2` returned HTTP 410 on the same day).
-- Repoint the `hive-embedding-default` alias's routing to OpenRouter for
-- both primary and fallback:
--
--   Primary  : route-openrouter-embedding           (Nemotron-Embed VL 1B, OR :free pool)
--   Fallback : route-openrouter-embedding-fallback  (Qwen3 Embedding 8B, OR paid)
--
-- The previous NVIDIA NIM route (`route-nvidia-embedding`) is marked
-- unhealthy and removed from the fallback cascade. The row stays in
-- provider_routes for historical traceability; a future migration can
-- delete it once we are certain no policy or audit query still
-- references it.

BEGIN;

-- 0. Widen the provider_routes.health_state CHECK constraint BEFORE the
--    UPDATE below sets health_state = 'eol'. The original constraint
--    only allowed {'healthy', 'degraded', 'unhealthy'}; updating to 'eol'
--    inside the same transaction would otherwise violate the check and
--    roll the entire migration back, leaving the alias policy half-
--    rewritten (and starving control-plane on startup migrations).
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM pg_constraint
         WHERE conrelid = 'public.provider_routes'::regclass
           AND conname  = 'provider_routes_health_state_check'
    ) THEN
        ALTER TABLE public.provider_routes
          DROP CONSTRAINT provider_routes_health_state_check;
        ALTER TABLE public.provider_routes
          ADD CONSTRAINT provider_routes_health_state_check
          CHECK (health_state IN ('healthy', 'degraded', 'disabled', 'eol'));
    END IF;
END $$;

-- 1. Register the new OpenRouter fallback route.
INSERT INTO public.provider_routes (
    route_id,
    alias_id,
    provider,
    provider_model,
    litellm_model_name,
    price_class,
    health_state,
    priority
) VALUES (
    'route-openrouter-embedding-fallback',
    'hive-embedding-default',
    'openrouter',
    'openrouter/qwen/qwen3-embedding-8b',
    'route-openrouter-embedding-fallback',
    'budget',
    'healthy',
    20
)
ON CONFLICT (route_id) DO UPDATE
   SET provider          = EXCLUDED.provider,
       provider_model    = EXCLUDED.provider_model,
       litellm_model_name = EXCLUDED.litellm_model_name,
       price_class       = EXCLUDED.price_class,
       health_state      = EXCLUDED.health_state,
       priority          = EXCLUDED.priority;

INSERT INTO public.provider_capabilities (
    route_id,
    supports_responses,
    supports_chat_completions,
    supports_completions,
    supports_embeddings,
    supports_streaming,
    supports_reasoning,
    supports_cache_read,
    supports_cache_write
) VALUES (
    'route-openrouter-embedding-fallback',
    false, false, false,
    true,
    false, false, false, false
)
ON CONFLICT (route_id) DO NOTHING;

-- 2. Promote the OpenRouter primary back to priority 10 (was demoted to
--    20 in 20260424_04 when NVIDIA NIM became the primary).
UPDATE public.provider_routes
   SET priority     = 10,
       health_state = 'healthy'
 WHERE route_id = 'route-openrouter-embedding';

-- 3. Quarantine the retired NIM route. Keep the row so historical
--    routing decisions remain decodable in audit replay; mark unhealthy
--    so SelectRoute will never pick it again.
UPDATE public.provider_routes
   SET health_state = 'eol',
       priority     = 999
 WHERE route_id = 'route-nvidia-embedding';

-- 4. Rewrite the alias policy's fallback chain to point at the two
--    OpenRouter routes only.
UPDATE public.alias_route_policies
   SET fallback_order = '["route-openrouter-embedding","route-openrouter-embedding-fallback"]'::jsonb
 WHERE alias_id = 'hive-embedding-default';

COMMIT;
