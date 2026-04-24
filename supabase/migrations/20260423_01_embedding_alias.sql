-- Seed the default embedding alias `hive-embedding-default` backed by
-- OpenRouter's free multimodal embedding model. The SDK embedding tests
-- (packages/sdk-tests/{python,js}/tests/**/test_embeddings*) default to
-- this alias when HIVE_TEST_MODEL is unset.
--
-- Upstream model: nvidia/llama-nemotron-embed-vl-1b-v2:free (text+image → embeddings)
-- Wired via deploy/litellm/config.yaml route `route-openrouter-embedding`,
-- which resolves $OPENROUTER_EMBEDDING_MODEL at LiteLLM startup.
--
-- Rate note: the free model is aggressively rate-limited; edge-api's
-- dispatch wrapper performs a bounded retry on 429/5xx
-- (see apps/edge-api/internal/inference/retry.go) so short-lived spikes
-- do not reach the client as hard 429s.

insert into public.model_aliases (
    alias_id,
    owned_by,
    display_name,
    summary,
    visibility,
    lifecycle,
    capability_badges,
    input_price_credits,
    output_price_credits,
    cache_read_price_credits,
    cache_write_price_credits
) values (
    'hive-embedding-default',
    'hive',
    'Hive Embedding Default',
    'Default multimodal embedding alias; backed by a free upstream tier.',
    'public',
    'stable',
    '["stable","embeddings"]'::jsonb,
    1,
    0,
    null,
    null
)
on conflict (alias_id) do nothing;

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
    'route-openrouter-embedding',
    'hive-embedding-default',
    'openrouter',
    'openrouter/nvidia/llama-nemotron-embed-vl-1b-v2:free',
    'route-openrouter-embedding',
    'budget',
    'healthy',
    10
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
    'route-openrouter-embedding',
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

insert into public.alias_route_policies (
    alias_id,
    policy_mode,
    allow_price_class_widening,
    fallback_order
) values (
    'hive-embedding-default',
    'pinned',
    false,
    '["route-openrouter-embedding"]'::jsonb
)
on conflict (alias_id) do nothing;
