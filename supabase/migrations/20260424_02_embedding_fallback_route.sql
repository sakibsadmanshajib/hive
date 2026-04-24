-- Add a NVIDIA NIM fallback route for the `hive-embedding-default` alias.
-- When OpenRouter's free embedding model is rate-limited / returns 5xx,
-- LiteLLM's router_settings.fallbacks cascades to `route-nvidia-embedding`
-- (deploy/litellm/config.yaml). Control-plane's alias policy also lists the
-- fallback so SelectRoute returns a non-empty FallbackRouteIDs set.

-- Widen the provider enum so routing repository tables can reference NIM.
alter table public.provider_routes
  drop constraint if exists provider_routes_provider_check;
alter table public.provider_routes
  add constraint provider_routes_provider_check
  check (provider in ('openrouter', 'groq', 'nvidia_nim'));

insert into public.provider_routes (
    route_id,
    alias_id,
    provider,
    provider_model,
    litellm_model_name,
    price_class,
    health_state,
    priority
) values (
    'route-nvidia-embedding',
    'hive-embedding-default',
    'nvidia_nim',
    'nvidia_nim/nvidia/llama-3.2-nemoretriever-300m-embed-v1',
    'route-nvidia-embedding',
    'budget',
    'healthy',
    -- Lower priority than route-openrouter-embedding (10) so the NVIDIA
    -- route is used only as a fallback.
    20
)
on conflict (route_id) do nothing;

insert into public.provider_capabilities (
    route_id,
    supports_responses,
    supports_chat_completions,
    supports_completions,
    supports_embeddings,
    supports_streaming,
    supports_reasoning,
    supports_cache_read,
    supports_cache_write
) values (
    'route-nvidia-embedding',
    false,
    false,
    false,
    true,
    false,
    false,
    false,
    false
)
on conflict (route_id) do nothing;

update public.alias_route_policies
   set fallback_order = '["route-openrouter-embedding","route-nvidia-embedding"]'::jsonb
 where alias_id = 'hive-embedding-default';
