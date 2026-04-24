create table public.provider_routes (
    route_id text primary key,
    alias_id text not null references public.model_aliases(alias_id) on delete cascade,
    provider text not null check (provider in ('openrouter', 'groq')),
    provider_model text not null,
    litellm_model_name text not null,
    price_class text not null check (price_class in ('budget', 'standard', 'premium')),
    health_state text not null check (health_state in ('healthy', 'degraded', 'disabled')) default 'healthy',
    priority integer not null
);

create table public.provider_capabilities (
    route_id text primary key references public.provider_routes(route_id) on delete cascade,
    supports_responses boolean not null default false,
    supports_chat_completions boolean not null default false,
    supports_completions boolean not null default false,
    supports_embeddings boolean not null default false,
    supports_streaming boolean not null default false,
    supports_reasoning boolean not null default false,
    supports_cache_read boolean not null default false,
    supports_cache_write boolean not null default false
);

create table public.alias_route_policies (
    alias_id text primary key references public.model_aliases(alias_id) on delete cascade,
    policy_mode text not null check (policy_mode in ('pinned', 'cost', 'latency', 'stability', 'weighted')),
    allow_price_class_widening boolean not null default false,
    fallback_order jsonb not null default '[]'::jsonb
);

insert into public.provider_routes (
    route_id,
    alias_id,
    provider,
    provider_model,
    litellm_model_name,
    price_class,
    health_state,
    priority
) values
    (
        'route-openrouter-default',
        'hive-default',
        'openrouter',
        'openrouter/openai/gpt-4o-mini',
        'route-openrouter-default',
        'standard',
        'healthy',
        10
    ),
    (
        'route-openrouter-auto',
        'hive-auto',
        'openrouter',
        'openrouter/openai/gpt-4.1-mini',
        'route-openrouter-auto',
        'premium',
        'healthy',
        10
    ),
    (
        'route-groq-fast',
        'hive-fast',
        'groq',
        'groq/llama-3.3-70b-versatile',
        'route-groq-fast',
        'standard',
        'healthy',
        10
    ),
    (
        'route-openrouter-fast-fallback',
        'hive-fast',
        'openrouter',
        'openrouter/meta-llama/3.1-8b-instruct',
        'route-openrouter-fast-fallback',
        'standard',
        'healthy',
        20
    );

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
) values
    (
        'route-openrouter-default',
        true,
        true,
        true,
        false,
        true,
        true,
        true,
        true
    ),
    (
        'route-openrouter-auto',
        true,
        true,
        true,
        false,
        true,
        true,
        true,
        true
    ),
    (
        'route-groq-fast',
        true,
        true,
        true,
        false,
        true,
        false,
        false,
        false
    ),
    (
        'route-openrouter-fast-fallback',
        true,
        true,
        true,
        false,
        true,
        false,
        false,
        false
    );

insert into public.alias_route_policies (
    alias_id,
    policy_mode,
    allow_price_class_widening,
    fallback_order
) values
    (
        'hive-default',
        'stability',
        false,
        '["route-openrouter-default"]'::jsonb
    ),
    (
        'hive-fast',
        'latency',
        false,
        '["route-groq-fast","route-openrouter-fast-fallback"]'::jsonb
    ),
    (
        'hive-auto',
        'weighted',
        true,
        '["route-openrouter-auto"]'::jsonb
    );
