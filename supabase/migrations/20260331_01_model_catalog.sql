create table public.model_aliases (
    alias_id text primary key,
    owned_by text not null default 'hive',
    display_name text not null,
    summary text not null,
    visibility text not null check (visibility in ('public', 'preview', 'internal')),
    lifecycle text not null check (lifecycle in ('stable', 'preview', 'hidden')),
    capability_badges jsonb not null default '[]'::jsonb,
    input_price_credits bigint not null,
    output_price_credits bigint not null,
    cache_read_price_credits bigint,
    cache_write_price_credits bigint,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

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
) values
    (
        'hive-default',
        'hive',
        'Hive Default',
        'Balanced default alias for everyday chat and responses workloads.',
        'public',
        'stable',
        '["stable","chat","responses"]'::jsonb,
        12,
        36,
        2,
        6
    ),
    (
        'hive-fast',
        'hive',
        'Hive Fast',
        'Low-latency alias for chat and responses requests that prioritize speed.',
        'public',
        'stable',
        '["fast","chat","responses"]'::jsonb,
        8,
        24,
        1,
        4
    ),
    (
        'hive-auto',
        'hive',
        'Hive Auto',
        'Preview alias that can widen internally within the Hive contract when needed.',
        'preview',
        'preview',
        '["auto","fallback","preview"]'::jsonb,
        10,
        30,
        1,
        5
    );
