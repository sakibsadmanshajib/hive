-- 20260830_01_openrouter_free_models_router.sql
--
-- Move the free pool's OpenRouter member off ONE pinned free model and onto
-- OpenRouter's own Free Models Router.
--
-- WHY. 20260824_02 pinned route-free-pool-free to
-- openrouter/dots-studio/dots-3-note-preview:free. OpenRouter adds and removes
-- free models constantly, so a pinned free model gets rate limited or retired
-- and nothing here notices: the deployment starts answering 404 or 429, the
-- edge retry ladder quietly fails the request over to a Groq or Gemini member,
-- and the OpenRouter slot in a four-key pool is dead with no alert anywhere.
--
-- RESEARCH, verified live against https://openrouter.ai/api/v1/models on
-- 2026-08-30 rather than recalled. That document lists 396 models, of which 21
-- price at exactly zero for both prompt and completion. Two of the entries are
-- routers rather than models:
--
--   openrouter/free  "Free Models Router", pricing prompt "0" completion "0",
--                    200k context, text+image input. Its own description: "a
--                    router that selects free models at random from the models
--                    available on OpenRouter".
--   openrouter/auto  "Auto Router", pricing prompt "-1" completion "-1", which
--                    is OpenRouter's marker for "billed at whatever the
--                    selected model costs". The Auto Router is PAID and is not
--                    a candidate here. It is already the separate hive-auto
--                    route (route-openrouter-auto-live).
--
-- So the free tier IS addressable as one id, and no discovery loop, no
-- periodic model list refresh and no hand maintained model list is needed.
-- The doubled prefix below is the same LiteLLM provider selector every sibling
-- OpenRouter row carries: LiteLLM consumes the leading openrouter/ and sends
-- openrouter/free upstream.
--
-- KNOWN CEILING, recorded because buying credit rather than writing code is
-- what lifts it. OpenRouter's free tier allows 20 requests per minute, and 50
-- requests per day while the account holds under 10 US dollars of lifetime
-- credit, rising to 1000 per day at or above that. Extra API keys do not raise
-- it; capacity is governed per account. Issue #1411 records our OpenRouter
-- balance at 1.59 of 10 dollars, so this member is in the 50 per day tier
-- today. That ceiling is upstream and is not ours to remove.
--
-- MONEY. hive-free is a FIXED price alias (1,000,000 credits per million input
-- tokens, 4,000,000 per million output, D-048), and routing resolves price from
-- the alias after selection, never from the selected route, so which pool
-- member answers is billing inert. provider_routes carries no price column at
-- all (D-032). That property is what makes pointing at a router safe here and
-- is what made issue #689 unsafe, where a free-model router sat behind a
-- variable-priced path; it is asserted in
-- apps/control-plane/internal/routing/free_pool_billing_inertness_test.go
-- rather than left as this comment.
--
-- CAPABILITY BECOMES NON-DETERMINISTIC ON THIS MEMBER. This migration
-- deliberately sets no capability flag, and the constraint is written here so
-- it is visible to whoever reviews the row that does.
--
-- Before: one pinned model, so every provider_capabilities flag on this route
-- was a statement about that model and was either true or false for good.
-- After: openrouter/free selects among the 21 zero-priced models per request,
-- and on OpenRouter both tool calling and response_format support are
-- per-model. So `tools_supported` and any structured-output claim are no longer
-- properties of the route, they are properties of whichever model answered.
--
-- The pool's members already declare tools_supported false, which stays the
-- safe direction and is what the hive-free alias summary already tells
-- customers. Whether this member belongs in a plain-chat group or should be
-- pinned to a tool-capable subset is a separate decision being made elsewhere,
-- and setting a flag here would collide with it.

UPDATE public.provider_routes
   SET provider_model = 'openrouter/openrouter/free'
 WHERE route_id = 'route-free-pool-free'
   AND provider_model <> 'openrouter/openrouter/free';

-- The reasoning reserve moves with the repoint. This is correctness
-- housekeeping on one row, NOT a live defect being fixed, and the difference
-- matters enough to state precisely, because an earlier draft of this comment
-- overstated it and a reviewer caught it.
--
-- 20260826_01 pinned this route at reasoning_reserve_tokens 0 and said why:
-- "openrouter's dots-studio/dots-3-note-preview:free does not reason, so its
-- deployments keep exactly the ceiling the caller asked for". That reason dies
-- with the pin. openrouter/free advertises `reasoning` and `include_reasoning`
-- in its supported_parameters and selects among whatever is free at the moment
-- of the request, so a reasoning-capable model can now answer here, and
-- provider_capabilities has declared supports_reasoning TRUE for this route
-- since 20260824_02. The row's 0 therefore asserts something that is no longer
-- true of the member.
--
-- What this does NOT change, stated plainly: the reserve the pool actually
-- applies. SelectRoute takes the MAXIMUM reserve across every eligible
-- candidate sharing the selection's litellm_model_name
-- (apps/control-plane/internal/routing/service.go:154-158), not the selected
-- member's own, and the three sibling members already carry 4096. The pool's
-- effective reserve was 4096 before this statement and is 4096 after it. The
-- answer changes only when this member is the SOLE eligible candidate, which
-- is the case where the other three are unhealthy or filtered out.
--
-- So: issue #1171's failure (hidden reasoning eats the caller's whole
-- max_tokens ceiling and the answer returns with empty visible content) is
-- what the column exists for, and it is reachable here only in that degenerate
-- single-member case. 4096 matches the siblings rather than inventing a figure,
-- and raising this member cannot lower the pool max, so there is no headroom
-- regression for the other three.

UPDATE public.provider_routes
   SET reasoning_reserve_tokens = 4096
 WHERE route_id = 'route-free-pool-free'
   AND reasoning_reserve_tokens <> 4096;
