-- =============================================================================
-- Restrict hive-free and hive-free-tools out of the tenant picker (issue:
-- rate-limited free pool + no-DPA anonymous upstream were both publicly
-- clickable).
--
-- THE DEFECT
--   Both aliases shipped visibility='public' (20260824_02_free_pool_router.sql,
--   20260830_04_opencode_zen_keyless_provider.sql), so any tenant could select
--   and invoke them from the chat model picker. Two reasons that is wrong:
--     1. hive-free load-balances across four Hive-owned free provider keys and
--        is the current source of sustained rate limiting: CI's Live
--        integration job answers "429 hive-free is temporarily rate limited"
--        today, and issue #1566 records the pool's Gemini member capped at 20
--        requests/day, 435 failures in 48h. A tenant picking it gets a 429.
--     2. hive-free-tools is served anonymously by a third party we hold no
--        account with -- no data processing agreement, no deletion path (its
--        own alias summary discloses this). That should not be one click away
--        in the picker.
--
-- THE FIX, AND WHY THIS MECHANISM
--   visibility='restricted' on both aliases. catalog.AliasVisibleToTenant
--   (apps/control-plane/internal/catalog/visibility.go) fails an alias closed
--   for every tenant with no explicit tenant_model_visibility(visible=true)
--   row, and it is the SAME predicate the catalog listing (the picker) and
--   routing.Service.SelectRoute (actual invocation, every inference surface:
--   JWT chat, RAG, audio, images, embeddings) both resolve through. So the
--   picker cannot disagree with what the API will actually accept.
--
--   This is deliberately NOT a picker-side id blocklist
--   (HIVE_PICKER_HIDDEN_MODEL_IDS or similar). A hardcoded id list lives in a
--   vendored handler, splices in at build time, and goes silently inert the
--   moment the compose bypass flag or the pinned image shifts -- that is
--   exactly how issue #776 died, and the owner has ruled against this shape
--   of band-aid. The visibility rail is schema-level and both surfaces read
--   it, so there is no second place for the two answers to drift apart.
--
-- NO GRANT ROWS HERE, ON PURPOSE
--   This file only flips the visibility column. It inserts zero
--   tenant_model_visibility rows: a schema migration runs identically against
--   every deployment (CI throwaway, the demo box, a future enterprise
--   install), and a specific tenant's UUID is per-deployment operational
--   data, not something a portable migration should encode. Restricted means
--   deny-by-default; granting the specific automated callers that are
--   SUPPOSED to keep using the free pool is handled at the layer that
--   provisions each of those callers:
--     * CI's live-integration job seeds its own throwaway tenant fresh on
--       every run (scripts/ci-seed-api-key.sh) and now grants that tenant
--       visible=true on both aliases in the same script, right where the
--       tenant is minted.
--     * The demo box's real, persistent verification tenant (sdk-replay in
--       deploy-demo-box.yml, scripts/post-deploy-verify.py,
--       scripts/verify-control-plane.py, all defaulting to hive-free) is NOT
--       granted by anything in this repo. It needs one operator action
--       against the deployed box, through the existing admin endpoint
--       (PUT /internal/catalog/visibility/{tenantID}/{aliasID}), before or
--       immediately after this migration deploys, or those checks start
--       failing. See the pull request body for the exact command; this is
--       intentionally not automated here, for the same per-deployment-data
--       reason the CI case IS handled in-repo and this one is not.
--
-- MODEL_POLICY_GROUP_MEMBERS IS UNTOUCHED
--   Both aliases stay in the 'default' and 'closed' policy groups. That table
--   gates which aliases an API key's PRICING TIER can see at all; visibility
--   gates which TENANT can see or invoke a given alias. They are independent
--   axes (an alias can be in every price tier's group and still be
--   tenant-restricted), and this fix only needed the second one.
--
-- RE-RUNNABILITY
--   Single UPDATE, guarded by a WHERE clause excluding rows already at the
--   target value. A second run affects zero rows and errors on nothing.
-- =============================================================================

BEGIN;

SET LOCAL lock_timeout = '5s';

UPDATE public.model_aliases
   SET visibility = 'restricted',
       updated_at = now()
 WHERE alias_id IN ('hive-free', 'hive-free-tools')
   AND visibility IS DISTINCT FROM 'restricted';

COMMIT;
