-- =============================================================================
-- Seed a real cache-write price for DeepSeek, closing the one gap the
-- cache-aware billing fix (edge-api CreditsForTokens) leaves in the existing
-- catalog (vault spec-2026-08-25-cache-aware-billing.md).
--
-- SCOPE, deliberately narrow. Every model_aliases row that carries a
-- cache_write_price_credits of exactly 0 today falls into one of two
-- categories, and this migration touches only the second:
--
--   1. Groq gpt-oss routes (hive-fast, hive-small, hive-medium, hive-default,
--      hive-auto): 20260822_02_catalog_alias_restructure.sql set BOTH cache
--      columns to an EXPLICIT 0 with a documented reason -- "Zero is the
--      correct value for a Groq gpt-oss route, which declares no cache
--      support and has no published cache rate." That is a considered,
--      sourced decision recorded in this repo, and this migration does not
--      override it. A later external source (this feature's own research
--      note) claims Groq DOES publish a cache rate (0.5x read, free write),
--      which conflicts with that prior finding; resolving the conflict needs
--      a live re-check against Groq's own docs, not a guess from either side,
--      so it is left as a follow-up rather than guessed here. Until then,
--      CreditsForTokens' fallback (resolveCacheRate in
--      apps/edge-api/internal/inference/pricing.go) covers these rows safely
--      if Groq ever does report cache tokens for them: it applies the
--      documented default multiplier and warns loudly rather than silently
--      reproducing the flat-rate bug or charging zero.
--
--   2. deepseek-v4-flash and deepseek-v4-pro: the SAME migration already
--      derived and seeded a real cache_read_price_credits for both (1790 and
--      5236 pre-rescale-unit credits respectively, 0.2x their input rate --
--      see that migration's DERIVE comment block), but left
--      cache_write_price_credits at 0 with NO accompanying rationale: the
--      DERIVE block never lists a cache_write row for either alias, so this
--      was an omission, not a decision. This migration fills it in.
--
-- WHY 1.0x (no premium, no discount) for DeepSeek specifically: unlike
-- Anthropic and OpenAI's cache-write premium (1.25x, because their cache
-- write is a real extra generation cost the provider passes through),
-- DeepSeek's published prompt-caching price sets cache-write at the same
-- rate as ordinary input (vault spec-2026-08-25-cache-aware-billing.md
-- section 2, sourced from DeepSeek's own pricing page). Deriving it as
-- `cache_write_price_credits = input_price_credits` rather than a literal
-- keeps it correct regardless of any future re-pricing of these two rows by
-- a later migration -- it always tracks whatever the row's own input price
-- is at the time this runs, the same self-referential-UPDATE pattern
-- 20260823_40_credit_unit_rescale_billion.sql already used for the unit
-- rescale.
--
-- Idempotent and narrow: the WHERE clause only touches a row that is both
-- one of these two specific aliases AND still carries the un-researched
-- placeholder (0), so re-running this migration, or running it after a
-- future repricing that already set a real value, is a no-op rather than a
-- second write.
BEGIN;

UPDATE public.model_aliases
   SET cache_write_price_credits = input_price_credits
 WHERE alias_id IN ('deepseek-v4-flash', 'deepseek-v4-pro')
   AND cache_write_price_credits = 0;

COMMIT;
