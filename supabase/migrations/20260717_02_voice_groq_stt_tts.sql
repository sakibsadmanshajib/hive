-- Wire serverless voice (STT + TTS) into routing for the cloud demo.
--
-- edge-api's audio handler (apps/edge-api/internal/audio/handler.go) already
-- routes POST /v1/audio/speech and POST /v1/audio/translations through the
-- generic LiteLLM route-selection path, and now falls back to the same path
-- for POST /v1/audio/transcriptions whenever no local sovereign STT sidecar
-- (Parakeet/faster-whisper) is configured. What was missing was catalog data:
-- no alias/route/capability rows existed for any TTS- or STT-capable route,
-- so SelectRoute always failed with "model not found" for a voice alias.
--
-- This adds two new aliases, each backed by a single Groq route (matching
-- deploy/litellm/config.yaml's route-groq-stt / route-groq-tts model_list
-- entries): whisper-large-v3 for transcription, canopylabs/orpheus-v1-english
-- for speech. Groq deprecated and shut down playai-tts on 2025-12-31 in favor
-- of Orpheus (Canopy Labs); orpheus-v1-english is the current recommended
-- replacement per https://console.groq.com/docs/deprecations.

insert into public.model_aliases (
    alias_id,
    owned_by,
    display_name,
    summary,
    visibility,
    lifecycle,
    capability_badges,
    input_price_credits,
    output_price_credits
) values
    (
        'hive-stt',
        'hive',
        'Hive Voice STT',
        'Serverless speech-to-text (Groq Whisper) for /v1/audio/transcriptions.',
        'public',
        'stable',
        '["voice","stt"]'::jsonb,
        0,
        500
    ),
    (
        'hive-tts',
        'hive',
        'Hive Voice TTS',
        'Serverless text-to-speech (Groq PlayAI) for /v1/audio/speech.',
        'public',
        'stable',
        '["voice","tts"]'::jsonb,
        0,
        1000
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
        'route-groq-stt',
        'hive-stt',
        'groq',
        'groq/whisper-large-v3',
        'route-groq-stt',
        'budget',
        'healthy',
        10
    ),
    (
        'route-groq-tts',
        'hive-tts',
        'groq',
        'groq/canopylabs/orpheus-v1-english',
        'route-groq-tts',
        'budget',
        'healthy',
        10
    );

insert into public.provider_capabilities (route_id, supports_stt)
values ('route-groq-stt', true);

insert into public.provider_capabilities (route_id, supports_tts)
values ('route-groq-tts', true);

insert into public.alias_route_policies (
    alias_id,
    policy_mode,
    allow_price_class_widening,
    fallback_order
) values
    ('hive-stt', 'pinned', false, '["route-groq-stt"]'::jsonb),
    ('hive-tts', 'pinned', false, '["route-groq-tts"]'::jsonb);

-- Same gap as 20260717_01_default_group_hive_auto.sql: allowed_group_names
-- defaults to ["default"], so a default-tier key never sees an alias unless
-- it is in the `default` group. Add the two voice aliases there so the demo
-- key can use them; ON CONFLICT DO NOTHING keeps this safe to re-run.
insert into public.model_policy_group_members (group_name, alias_id)
values ('default', 'hive-stt'), ('default', 'hive-tts')
on conflict (group_name, alias_id) do nothing;

-- Data-integrity fix, same table: 20260414_01_provider_capabilities_media_columns.sql
-- set supports_tts/supports_stt = true on route-openrouter-auto when it added
-- the columns, but that route's model (openrouter/openai/gpt-4.1-mini, a
-- vision chat model) has never had real TTS/STT capability. Left uncorrected,
-- SelectRoute could pick this route for a NeedTTS/NeedSTT request on the
-- hive-auto alias and fail upstream instead of returning a clean
-- model-not-found. Voice now has real routes (above), so this false flag
-- serves no purpose.
update public.provider_capabilities
set supports_tts = false, supports_stt = false
where route_id = 'route-openrouter-auto';
