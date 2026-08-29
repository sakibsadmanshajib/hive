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

-- ONE vocabulary for this whole file. It previously carried three, none of
-- them equal to the Go guard: a five token update predicate, an eleven token
-- census, and providerblind.go's eleven tokens plus five patterns. The
-- divergence had a concrete cost. A hive-stt summary someone had since edited
-- to read "Serverless speech-to-text via NVIDIA NIM" failed the update
-- predicate, so the row was neither repaired nor reported, while the boundary
-- dropped the description on every request. The outcome is the permanently
-- blank description this file exists to prevent, arrived at silently.
--
-- Kept identical to providerIdentityRegex in
-- apps/control-plane/internal/catalog/providerblind.go and its edge-api twin.
-- \m and \M are Postgres's start-of-word and end-of-word constraints, the ARE
-- spelling of Go's \b, and the token group deliberately carries no trailing
-- \M for the same reason the Go one carries no trailing \b: GroqCloud is
-- Groq's own product name and a trailing boundary loses it.
--
-- NOTICE and not an exception throughout, on purpose: an admin-seeded row must
-- not be able to wedge the migration chain, and the enforcement point is the
-- boundary guard rather than this file.
do $$
declare
  provider_pattern constant text :=
    '\m(groq|openrouter|litellm|cerebras|fireworks|deepinfra|sambanova|novita|hyperbolic|perplexity|bedrock)'
    || '|\mnvidia[ _-]?nim\M'
    || '|\mtogether[ ._-]?ai\M'
    || '|\mvertex[ _-]?ai'
    || '|\mgoogle[ _-]?vertex'
    || '|\mazure[ _-]?(openai|ai|ml)\M'
    || '|\mroute-[a-z0-9][a-z0-9._/-]*';
  repaired integer;
  offending record;
begin
  -- The predicate is what makes these two safe to re-run and safe against
  -- curation: a row is rewritten only while it still names a provider, so a
  -- summary someone has since edited by hand is left exactly as it is.
  update public.model_aliases
  set summary = 'Serverless speech-to-text for /v1/audio/transcriptions.'
  where alias_id = 'hive-stt'
    and summary ~* provider_pattern;
  get diagnostics repaired = row_count;
  raise notice 'hive-stt summary rows repaired: %', repaired;

  update public.model_aliases
  set summary = 'Serverless text-to-speech for /v1/audio/speech.'
  where alias_id = 'hive-tts'
    and summary ~* provider_pattern;
  get diagnostics repaired = row_count;
  raise notice 'hive-tts summary rows repaired: %', repaired;

  -- Say out loud whether anything else in the customer-facing catalogue still
  -- names a serving provider, rather than leaving it to be discovered by
  -- another SDK run. The boundary guard already redacts these four on the way
  -- out, so a row listed here is a copy repair to schedule, not a live leak.
  --
  -- `is distinct from` rather than `<>` because visibility is nullable and
  -- NULL <> 'internal' is NULL, which would drop exactly the rows nobody has
  -- classified yet.
  for offending in
    select alias_id, display_name, summary, owned_by, capability_badges
    from public.model_aliases
    where visibility is distinct from 'internal'
      and (
        display_name ~* provider_pattern
        or summary ~* provider_pattern
        or owned_by ~* provider_pattern
        or capability_badges::text ~* provider_pattern
      )
    order by alias_id
  loop
    -- capability_badges is printed as well as matched. It was matched and not
    -- printed, so a row that leaked only through a badge reported four clean
    -- looking fields and read as a regex false positive, which is how a real
    -- leak gets dismissed.
    raise notice 'model_aliases row still names a serving provider in customer-facing copy: alias_id=% display_name=% owned_by=% capability_badges=% summary=%',
      offending.alias_id, offending.display_name, offending.owned_by, offending.capability_badges, offending.summary;
  end loop;

  -- alias_id gets its own pass, and it is the one that actually needs a human.
  -- The four fields above are precisely what the boundary guard repairs
  -- automatically; alias_id is excluded from that redaction on purpose,
  -- because the id is the customer's invocation handle and published contract,
  -- so renaming it takes its own migration and a deprecation. It was also the
  -- only one the operator was never told about.
  --
  -- The visibility filter is dropped here deliberately. openrouter-auto is
  -- internal today, and 20260822_30_openrouter_auto_variable_pricing.sql says
  -- flipping it to 'public' is a one-line follow-up migration. Without this
  -- pass the sequence is: somebody runs that one-liner, the boundary writes a
  -- line to a container stdout nobody is tailing, this migration has already
  -- run and will not run again, and {"id":"openrouter-auto"} is on the
  -- unauthenticated /catalog/models with nothing having said a word.
  for offending in
    select alias_id, visibility
    from public.model_aliases
    where alias_id ~* provider_pattern
    order by alias_id
  loop
    raise notice 'model_aliases alias_id itself names a serving provider and is published verbatim as the model id: alias_id=% visibility=%. The boundary guard cannot repair this one, renaming it needs its own migration.',
      offending.alias_id, offending.visibility;
  end loop;
end
$$;

commit;
