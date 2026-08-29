-- Strip provider identity out of the two customer-facing voice alias summaries.
--
-- model_aliases.summary is served verbatim as the `description` field of every
-- GET /v1/models entry, so it is a customer-facing surface and the
-- provider-blind invariant applies to it exactly as it does to error bodies.
-- 20260717_02_voice_groq_stt_tts.sql seeded these two rows with the upstream
-- named in prose ("Groq Whisper", "Groq PlayAI"), which leaked the provider to
-- every caller that lists models. The committed SDK conformance suite caught
-- it live: packages/sdk-tests/js/tests/models/list-models.test.ts asserts the
-- serialized /v1/models body does not match /openrouter|groq/i, and it failed
-- on exactly these two descriptions.
--
-- The replacement wording keeps the capability and the endpoint, which is what
-- a caller actually needs, and drops the upstream, which is ours to change
-- without telling anyone. The PlayAI reference was stale as well: that model
-- was shut down on 2025-12-31 and the route already serves Orpheus.
--
-- Scoped by both alias_id and the exact old text, so this cannot overwrite a
-- summary an operator has since retuned by hand.

update public.model_aliases
   set summary = 'Serverless speech-to-text for /v1/audio/transcriptions.'
 where alias_id = 'hive-stt'
   and summary = 'Serverless speech-to-text (Groq Whisper) for /v1/audio/transcriptions.';

update public.model_aliases
   set summary = 'Serverless text-to-speech for /v1/audio/speech.'
 where alias_id = 'hive-tts'
   and summary = 'Serverless text-to-speech (Groq PlayAI) for /v1/audio/speech.';
