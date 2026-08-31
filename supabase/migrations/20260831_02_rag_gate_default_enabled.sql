-- supabase/migrations/20260831_02_rag_gate_default_enabled.sql
--
-- Issue #1506: /v1/rag/* answers 403 for any tenant that has no explicit
-- ENABLE_RAG row in public.tenant_settings.
--
-- The gate registry gained a per-key default in
-- 20260824_01_cowork_gate_default_enabled.sql, which flipped ENABLE_COWORK for
-- exactly the same reason and deliberately left every other key opt-in. RAG was
-- correct to stay opt-in while it had no consumer in the product: issue #1506's
-- own verdict was that the route was unexercised rather than broken, and only
-- tenants hand seeded by scripts/seed-demo-owner.py had the row.
--
-- Issue #1595 removes that premise. Projects resolves to this store, so the
-- chat surface now depends on the route, and "unset reads as false" would mean
-- a workspace nobody hand seeded cannot use Projects at all until an
-- administrator writes a row. That is the #1506 regression, and it is now a
-- demo blocker rather than an observation (D-061 puts knowledge work in demo
-- scope).
--
-- What this changes for existing tenants:
--   * A tenant with an explicit ENABLE_RAG row, either true or false, is
--     unaffected. Explicit rows always win over the declared default, which is
--     the COALESCE(s.enabled, k.default_enabled) in
--     apps/control-plane/internal/tenant/settings/resolver.go.
--   * A tenant with no row moves from RAG refused to RAG available. No data is
--     created or exposed by that alone: every /v1/rag/* route still requires an
--     authenticated caller and every row is still tenant scoped by RLS, so this
--     widens a feature gate and never an authentication or isolation boundary.
--   * An administrator can still turn RAG off per workspace by writing an
--     enabled=false row through the existing feature-gate admin surface.
--
-- Deliberately one key and one column, in its own migration, so it can be
-- reverted alone without touching the Projects schema.
--
-- Depends on: 20260824_01_cowork_gate_default_enabled.sql (default_enabled)
--             20260625_04_carl_featuregate_keys.sql (the ENABLE_RAG key row)

BEGIN;

UPDATE public.feature_gate_keys
   SET default_enabled = true
 WHERE key = 'ENABLE_RAG';

COMMIT;
