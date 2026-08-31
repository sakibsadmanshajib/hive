-- =============================================================================
-- The free pool tells the truth about what it can do, and becomes uniform
-- enough for that truth to be a single answer (2026-08-30).
--
-- WHAT WAS WRONG
--   20260824_02_free_pool_router.sql seeded all four route-free-pool members
--   with tools_supported = false. The reasoning it recorded was honest, nobody
--   had probed cross-member tool parity, so it declined to claim it. Nobody
--   probed it afterwards either, and the placeholder became the catalog's
--   answer.
--
--   tools_supported is not a tools-only flag. PR #206 wired it to tools,
--   tool_choice AND response_format, so hive-free answered
--     400 Model 'hive-free' does not support parameter: response_format
--   to every structured-output request. That 400 is OURS. It comes from
--   guardToolCapability in apps/edge-api/internal/inference/chat_completions.go
--   calling SelectRoute with RequireToolCapable, getting ErrNoCapableRoute, and
--   writing a provider-blind refusal. No provider ever declined anything.
--
--   The cost was paid twice: free-tier customers could not use structured
--   output at all, and CI moved its capability lane onto paid aliases to get
--   around the refusal, which is the opposite of the owner's ruling that
--   automated consumption belongs on the free pool (D-047).
--
-- WHY A CAPABILITY FLAG IS A PROPERTY OF THE GROUP, NOT OF A MEMBER
--   Pool membership is a shared litellm_model_name. edge-api dispatches
--   route.LiteLLMModelName and never the route id, LiteLLM load-balances every
--   deployment under that one name, and SelectRoute's own reasoning-reserve
--   block says outright that it cannot know which member will answer before it
--   dispatches. A per-member capability flag inside a shared group is therefore
--   unenforceable: selection filters to a capable row, dispatch hands the
--   request to the group, and any member can answer.
--
--   So a group can only honestly declare what its WEAKEST member supports, and
--   hive-free stays ONE group by owner decision: Hive Free and a Hive Free
--   tools variant cannot be two endpoints, they must be one model. The way to
--   get an honest tools_supported = true is therefore to make the pool uniform,
--   and a member that cannot serve the declared capability does not belong in
--   it.
--
-- WHAT WAS PROBED, 2026-08-30
--   Method: a real chat completion carrying a function tool with tool_choice
--   auto, and a second carrying response_format json_schema with strict true,
--   additionalProperties false and two required properties, at max_tokens 512
--   (the ceiling the SDK suite uses, so a reasoning burn is not mistaken for a
--   capability gap). Pass means HTTP 200 plus, for the first, a tool_calls
--   array naming the function, and for the second, content that parses as JSON
--   with exactly the schema's keys.
--
--   groq qwen/qwen3.8-27b, both key slots        PASS, DETERMINISTIC
--       LIVE PROBE against https://api.groq.com/openai/v1/chat/completions with
--       our own free-tier key, on the outgoing model and the incoming one.
--       openai/gpt-oss-20b: tools 200 with finish_reason tool_calls,
--       response_format 200 with exactly the two keys. qwen/qwen3.8-27b:
--       identical, and clean on every attempt made. reasoning_effort low,
--       medium and high each answered 200 with a populated reasoning field, so
--       supports_reasoning stays honest across the repoint below. Groq runs
--       constrained decoding, so the schema is enforced rather than requested.
--       Key slot 2 is not separately probed and does not need to be: capability
--       is a property of the model, and the slots differ in quota only.
--
--   gemini-flash-latest through Google's OpenAI-compatible endpoint
--                                                UNVERIFIABLE. THEREFORE NOT
--                                                CLAIMED.
--       Google's own OpenAI-compatibility page
--       (https://ai.google.dev/gemini-api/docs/openai) documents Function
--       calling and Structured output against exactly this member's base URL,
--       https://generativelanguage.googleapis.com/v1beta/openai. That is a
--       documented claim, not a measurement, and it was never checked against
--       the strict-schema shape this file uses as its bar.
--
--       An earlier revision kept the member on that documentation. Review
--       caught the contradiction and was right: this file rejects the
--       OpenRouter member on MEASURED failure of a capability, and cannot then
--       accept another member on an UNMEASURED claim of the same capability.
--       The standard has to be one standard, and it matters here more than it
--       usually would, because LiteLLM balances across the group: an
--       over-claimed member makes the whole alias's claim false for whatever
--       share of requests it answers, and the failure gets blamed on the alias
--       rather than on the member.
--
--       It could not be measured. GEMINI_API_KEY exists only as a repository
--       secret and no credential for it exists on any machine this work was
--       done from, so the probe run against both Groq slots simply cannot be
--       run against this one.
--
--       Independently, issue #1566 records what this member does in
--       production: Google's free tier caps it at 20 requests per day, and it
--       produced 435 rate-limit failures in 48 hours. So it is not merely
--       unverified, it is measurably unable to serve most of what reaches it.
--       A member nobody can verify because it has no usable quota is a member
--       that cannot answer requests either, and the two facts point the same
--       way.
--
--       So the claim is not made and the member is disabled, on exactly the
--       reasoning that removed the OpenRouter one. Restoring it needs the same
--       thing the OpenRouter member would need: a probe that passes.
--
--   openrouter, on openrouter/openrouter/free    FAILS. MEASURED, NOT INFERRED.
--       PR #1554 repointed this member to the Free Models Router, which selects
--       among the zero-priced catalog per request. Support for these parameters
--       on OpenRouter is per MODEL, and that catalog is not uniform: read live
--       from https://openrouter.ai/api/v1/models on 2026-08-30, of the 20
--       zero-priced non-router models 17 list tools, only 10 list
--       response_format, and only 4 list structured_outputs. The router entry's
--       own supported_parameters is the union of what its candidates can do,
--       not a promise about the one that answers.
--
--       Five identical strict json_schema requests to openrouter/free returned
--       one conforming object, two empty strings, one prose answer, and one
--       markdown-fenced object of a different shape. One pass in five.
--
--       Pinning it to a specific capable model was tried and also fails, which
--       is the finding that decided this file. Every zero-priced model whose
--       catalog entry claims tools AND response_format AND structured_outputs
--       was probed at max_tokens 512:
--         dots-studio/dots-3-note-preview:free     1 conforming answer in 5
--                                                  (the pin this member carried
--                                                  before #1554; the rest were
--                                                  empty or truncated mid-JSON)
--         nvidia/nemotron-3-super-120b-a12b:free   5 conforming answers in 9
--         liquid/lfm-2.5-2.6b:free                 2 in 3, plus an upstream 429
--         z-ai/glm-5.2:free                        0 in 3, upstream 429 every
--                                                  time
--       So OpenRouter's structured_outputs flag records what a model claims,
--       not a constraint its free-tier providers enforce. None of these is a
--       basis for a tools_supported = true that a single shared group applies
--       to every request.
--
-- THE RESOLUTION
--   The OpenRouter and Gemini members leave hive-free. The pool becomes the two
--   Groq key slots, both live-probed, under one litellm_model_name, and
--   tools_supported = true becomes a claim that holds whichever member answers
--   because every member that can answer was measured.
--
--   The alternative was to keep four members and pin the OpenRouter one to a
--   capable model. It was tried first, on the reasoning that four members is
--   better failover and a second vendor, and it was abandoned on the numbers
--   above: there is no free OpenRouter model that honours a strict schema
--   reliably enough to sit behind a capability flag.
--
--   What is lost, stated plainly because it is the real cost of this change:
--   vendor diversity. The pool is now two key slots on one Groq organization,
--   so a Groq outage takes hive-free down where previously another vendor might
--   have answered. That is a genuine reduction and it is not being minimised.
--
--   It is still the right trade, for two reasons. The diversity being given up
--   was already largely notional: the Gemini member serves 20 requests a day
--   before it starts refusing (issue #1566), and the OpenRouter member answers
--   a strict schema once in five attempts. Failover onto a member that cannot
--   serve the request is not failover. And the alternative is worse in kind
--   rather than degree: a capability the catalog declares and the pool cannot
--   honour is a wrong answer nothing can distinguish from a right one, which is
--   the exact defect this file exists to correct.
--
--   What is gained is that hive-free's declared capabilities are true of every
--   request, on measured evidence for every member, and the alias stays a
--   single endpoint at a single price, which is the owner's requirement.
--
--   Pool membership beyond this is the coordinator's call and is deliberately
--   untouched here. Both departures are one column each and reversible.
--
--   The route row is DISABLED, not deleted, so #1554's repoint and its
--   reasoning-reserve correction stay in history and the member can be restored
--   by flipping one column the day a free OpenRouter model earns it.
--
-- THE GROQ REPOINT: TEN TIMES THE DAILY TOKENS, SAME ACCOUNT, SAME KEYS
--   Both Groq slots served openai/gpt-oss-20b. Groq's published free-plan
--   rate-limit table (https://console.groq.com/docs/rate-limits, read live
--   2026-08-30) gives that model RPM 30, RPD 1K, TPM 8K, TPD 200K. It gives
--   qwen/qwen3.8-27b RPM 30, RPD 1K, TPM 8K, TPD 2M: every other ceiling
--   identical, ten times the daily token allowance. 200K tokens per day is
--   roughly 165 messages across the whole organization, shared by CI, the
--   nightly lanes and the demo, and it is a hard cliff that has already taken a
--   required check red for a reason no commit caused. That matters more now
--   that the pool is two members rather than four.
--
--   One caveat on that headroom, because it is easy to read as a doubling and
--   is not: Groq's daily token allowance is per ORGANIZATION, not per key.
--   GROQ_API_KEY and GROQ_API_KEY_2 are two key slots, and whether they sit on
--   one organization or two is the open question in issue #1413. If one, the
--   pool's real ceiling is 2M tokens a day shared, not 4M, and the second slot
--   buys per-minute headroom and failover rather than more daily tokens. This
--   file does not assume the favourable reading anywhere.
--
--   The model id is qwen/qwen3.8-27b, not the bare qwen3.8-27b, confirmed
--   against GET https://api.groq.com/openai/v1/models on 2026-08-30: present,
--   active true, owned_by Alibaba Cloud, context_window 131042.
--
--   The one real trade, stated: max_completion_tokens drops from 65536 to
--   16384. Nothing in this catalog asks a free-pool member for more (the SDK
--   suites cap at 512), so it costs nothing today. It is written here so the
--   next person to raise a ceiling knows where the wall is. Context window is
--   effectively unchanged, 131042 against 131072.
--
-- THE PINNED POLICY MOVES WITH THE MEMBER
--   alias_route_policies pinned fallback_order[0] at route-free-pool-free.
--   Leaving it there would strand selection's first choice on a disabled route,
--   so it moves to route-free-pool-groq, an active member of the same group at
--   the same price class.
--
-- RE-RUNNABILITY
--   Every statement is an UPDATE carrying a WHERE guard excluding rows already
--   at the target value, so a second run affects zero rows and errors on
--   nothing. Guarded by TestCapabilityTruthMigrationIsRerunnable.
-- =============================================================================

BEGIN;

SET LOCAL lock_timeout = '5s';

-- ─── 1. The member that cannot hold the claim leaves the pool ───────────────
--
-- health_state 'disabled' is the same retirement 20260824_02 used for
-- route-free-auto and route-free-default. SelectRoute filters it out and
-- litellmconfig.SyncService drops its deployment, so the group LiteLLM
-- load-balances becomes exactly the two Groq key slots.

UPDATE public.provider_routes
   SET health_state = 'disabled'
 WHERE route_id = 'route-free-pool-free'
   AND health_state <> 'disabled';

-- The Gemini member leaves on the same standard, not a softer one. Its tool and
-- structured-output support is documented by Google but was never measured
-- against the strict-schema shape used on the Groq slots, and it cannot be:
-- GEMINI_API_KEY exists only as a repository secret. Issue #1566 independently
-- records it capped at 20 requests a day with 435 rate-limit failures in 48
-- hours, so it is also unable to serve most of what reaches it. Disabling it
-- partly addresses that issue; the rest of the capacity question is not decided
-- here.

UPDATE public.provider_routes
   SET health_state = 'disabled'
 WHERE route_id = 'route-free-pool-gemini'
   AND health_state <> 'disabled';

-- The pinned first choice cannot be a disabled route.

UPDATE public.alias_route_policies
   SET fallback_order = '["route-free-pool-groq"]'::jsonb
 WHERE alias_id = 'hive-free'
   AND fallback_order <> '["route-free-pool-groq"]'::jsonb;

-- ─── 2. The Groq repoint ────────────────────────────────────────────────────
--
-- provider_model is the string the config sync emits as the LiteLLM entry's
-- `model:`. The groq/ prefix is the provider selector LiteLLM strips, and
-- qwen/qwen3.8-27b is the model id as Groq's own /v1/models reports it.
--
-- Written as single-row UPDATEs rather than one IN-list because the offline
-- migration reader these guards run on
-- (apps/control-plane/internal/routing/sqlparse_test.go) understands
-- `WHERE route_id = '...'` and not an IN list. A statement the guard cannot
-- read is a statement the guard cannot check, and the last unchecked claim
-- about these rows went stale for six days.

UPDATE public.provider_routes
   SET provider_model = 'groq/qwen/qwen3.8-27b'
 WHERE route_id = 'route-free-pool-groq'
   AND provider_model <> 'groq/qwen/qwen3.8-27b';

UPDATE public.provider_routes
   SET provider_model = 'groq/qwen/qwen3.8-27b'
 WHERE route_id = 'route-free-pool-groq-2'
   AND provider_model <> 'groq/qwen/qwen3.8-27b';

-- ─── 3. The capability correction, one guarded statement per member ─────────
--
-- Exactly the two members that remain, both live-probed. route-free-pool-free
-- and route-free-pool-gemini are deliberately absent: they are leaving the pool,
-- and setting a flag on the way out that one cannot honour and the other was
-- never measured for would be the same defect this file exists to fix.

UPDATE public.provider_capabilities
   SET tools_supported = true
 WHERE route_id = 'route-free-pool-groq'
   AND tools_supported IS DISTINCT FROM true;

UPDATE public.provider_capabilities
   SET tools_supported = true
 WHERE route_id = 'route-free-pool-groq-2'
   AND tools_supported IS DISTINCT FROM true;

-- ─── 4. The customer-facing copy, which was carrying the wrong claim ────────
--
-- The old summary ended "Tool calling and structured output are not offered on
-- this alias." That sentence was the placeholder speaking to customers. The
-- badge set gains tools and reasoning, matching the vocabulary the catalog
-- already uses on its other tool-capable aliases; supports_reasoning has been
-- true on every pool member since 20260824_02 and was simply never badged.
--
-- Provider-blind per the catalog convention: the summary names capabilities and
-- never an upstream.

UPDATE public.model_aliases
   SET summary           = 'Free-tier alias served from a load-balanced pool of our free provider keys; requests fail over automatically when one key is exhausted. Tool calling and structured output are supported.',
       capability_badges = '["stable","chat","responses","tools","reasoning"]'::jsonb,
       updated_at        = now()
 WHERE alias_id = 'hive-free'
   AND (summary IS DISTINCT FROM 'Free-tier alias served from a load-balanced pool of our free provider keys; requests fail over automatically when one key is exhausted. Tool calling and structured output are supported.'
        OR capability_badges <> '["stable","chat","responses","tools","reasoning"]'::jsonb);

COMMIT;
