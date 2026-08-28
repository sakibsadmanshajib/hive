-- Issue #1284: GET /v1/models leaked the upstream serving provider in the
-- hive-stt and hive-tts descriptions.
--
-- Confirmed live against https://api-hive.scubed.co/v1 through the OpenAI SDK:
--   {"id":"hive-stt", ..., "description":"Serverless speech-to-text (Groq Whisper) for /v1/audio/transcriptions."}
--   {"id":"hive-tts", ..., "description":"Serverless text-to-speech (Groq PlayAI) for /v1/audio/speech."}
--
-- Both strings were seeded by 20260717_02_voice_groq_stt_tts.sql into
-- public.model_aliases.summary, which the catalogue publishes verbatim as the
-- `description` field of GET /v1/models and as `summary` on the console
-- catalogue endpoint. That breaks the standing invariant in CLAUDE.md:
-- provider names never reach a customer.
--
-- The hive-tts text was also stale on its own terms. Groq deprecated playai-tts
-- on 2025-12-31 and the route seeded by that same migration has pointed at
-- canopylabs/orpheus-v1-english ever since, so the description named a model
-- that has not served a request in this system.
--
-- This file repairs today's two rows. It is NOT the guard: the guard is at the
-- boundary, in apps/control-plane/internal/catalog/providerblind.go and
-- apps/edge-api/internal/catalog/providerblind.go, which scrub these fields on
-- the way out so a row seeded later cannot leak the same way. Both are needed:
-- the boundary keeps the payload clean, and this restores the useful sentence
-- that the boundary would otherwise have to drop.
--
-- The predicate is what makes this safe to re-run and safe against curation: a
-- row is rewritten only while it still names a provider, so a summary someone
-- has since edited by hand is left exactly as it is.

begin;

update public.model_aliases
set summary = 'Serverless speech-to-text for /v1/audio/transcriptions.'
where alias_id = 'hive-stt'
  and summary ~* '\m(groq|openrouter|litellm|cerebras|bedrock)\M';

update public.model_aliases
set summary = 'Serverless text-to-speech for /v1/audio/speech.'
where alias_id = 'hive-tts'
  and summary ~* '\m(groq|openrouter|litellm|cerebras|bedrock)\M';

-- Say out loud whether anything else in the customer-facing catalogue still
-- names a serving provider, rather than leaving it to be discovered by another
-- SDK run. The boundary guard already redacts these on the way out, so a row
-- listed here is a copy repair to schedule, not a live leak. NOTICE and not an
-- exception on purpose: an admin-seeded row must not be able to block the
-- migration chain, and the vocabulary here is a snapshot of the Go one rather
-- than the enforcement point.
do $$
declare
  offending record;
begin
  for offending in
    select alias_id, display_name, summary, owned_by
    from public.model_aliases
    where visibility <> 'internal'
      and (
        display_name ~* '\m(groq|openrouter|litellm|cerebras|bedrock|perplexity|fireworks|deepinfra|sambanova|novita|hyperbolic)\M'
        or summary ~* '\m(groq|openrouter|litellm|cerebras|bedrock|perplexity|fireworks|deepinfra|sambanova|novita|hyperbolic)\M'
        or owned_by ~* '\m(groq|openrouter|litellm|cerebras|bedrock|perplexity|fireworks|deepinfra|sambanova|novita|hyperbolic)\M'
        or capability_badges::text ~* '\m(groq|openrouter|litellm|cerebras|bedrock|perplexity|fireworks|deepinfra|sambanova|novita|hyperbolic)\M'
      )
    order by alias_id
  loop
    raise notice 'model_aliases row still names a serving provider in customer-facing copy: alias_id=% display_name=% owned_by=% summary=%',
      offending.alias_id, offending.display_name, offending.owned_by, offending.summary;
  end loop;
end
$$;

commit;
