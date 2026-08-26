-- Reasoning headroom reserve per pool member (issue #1171).
--
-- WHY
--   The hive-free alias load-balances four heterogeneous members through one
--   LiteLLM model group. Two of them are reasoning models: a reasoning member
--   can spend the caller's entire max_tokens on HIDDEN reasoning, return
--   finish_reason=length with no visible content at all, and the request still
--   settles as a full-price success (live evidence on #1171: five of six
--   reasoning prompts returned empty content and were billed in full).
--
--   The fix's dispatch half is per-member HEADROOM: when an alias's pool
--   carries a positive reserve, edge-api inflates the max_tokens it sends
--   upstream by that reserve, so hidden reasoning burns the reserve and the
--   caller's own budget survives as visible content. The caller's requested
--   max_tokens keeps its meaning: it caps what they SEE, not what the model
--   thinks.
--
-- WHERE THE NUMBER LIVES
--   provider_capabilities.supports_reasoning cannot carry this: all four free
--   pool members share identical capability rows (20260824_02 set
--   supports_reasoning TRUE pool-wide, including the non-reasoning dots
--   member, because an under-claim would 422 working requests). So the
--   reserve is its own column on provider_routes -- the member row -- with a
--   default of 0 meaning "no reserve; send the caller's ceiling untouched".
--   Only routes whose upstream model family actually reasons carry a nonzero
--   value.
--
-- THE VALUE
--   4096 tokens. Bounded, generous for gpt-oss-20b and gemini-flash thinking
--   traces at free-tier prompt sizes, and cheap: inflating a ceiling costs
--   nothing unless the model actually generates the extra tokens, which on a
--   non-reasoning member it does not (it stops at EOS first). Inflation is
--   applied by edge-api at dispatch against the MAX reserve across the
--   selected pool (routing.SelectionResult.reasoning_reserve_tokens), so a
--   mixed pool needs only the reasoning members to carry values here.
--
-- RE-RUNNABILITY
--   The ADD COLUMN is guarded (IF NOT EXISTS) and both UPDATEs carry WHERE
--   guards excluding rows already at the target value, so a second run
--   affects zero rows.

BEGIN;

SET LOCAL lock_timeout = '5s';

alter table public.provider_routes
  add column if not exists reasoning_reserve_tokens integer not null default 0;

-- The three reasoning members of the hive-free pool (route ids pinned by
-- apps/control-plane/internal/routing/free_pool_router_test.go):
--   route-free-pool-gemini  gemini/gemini-flash-latest (thinking-capable)
--   route-free-pool-groq    groq/openai/gpt-oss-20b
--   route-free-pool-groq-2  groq/openai/gpt-oss-20b (key slot 2)
update public.provider_routes
set reasoning_reserve_tokens = 4096
where route_id in (
    'route-free-pool-gemini',
    'route-free-pool-groq',
    'route-free-pool-groq-2'
)
  and reasoning_reserve_tokens <> 4096;

-- The fourth member stays at the default 0: openrouter's
-- dots-studio/dots-3-note-preview:free does not reason, so its deployments
-- keep exactly the ceiling the caller asked for.
update public.provider_routes
set reasoning_reserve_tokens = 0
where route_id = 'route-free-pool-free'
  and reasoning_reserve_tokens <> 0;

COMMIT;
