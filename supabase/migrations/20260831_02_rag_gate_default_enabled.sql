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
--   * A tenant with no row moves from RAG refused to RAG available. No
--     authentication or isolation boundary moves with it: featuregate's
--     requireFunc resolves the principal and denies when there is none, so an
--     unauthenticated request never benefits from a true default, FeatureRAG has
--     exactly one enforcement site (gated_routes.go), and every row stays tenant
--     scoped.
--   * A cost surface does move, and it is not free. Handler.WithBilling wires
--     the money path for POST /v1/rag/chat only: sessionbilling.Start is called
--     from chat_handler.go and nowhere else, so document ingest and the query
--     embedding on POST /v1/rag/search reach the embedding backend with no
--     credit hold, no charge and no per tenant corpus quota, bounded only by a
--     per request byte cap. budgetGate does not cover the difference, because it
--     resolves identity from an API key bearer and stays inert for the JWT
--     traffic these routes serve. Before this migration that spend was reachable
--     only on tenants scripts/seed-demo-owner.py had seeded; after it, every
--     authenticated user of every workspace can drive it, including an account
--     at zero credits. That is an accepted risk taken to unblock issue #1506 and
--     issue #1595, not an absence of one, and it is tracked in issue #1644.
--   * An administrator can still turn RAG off per workspace by writing an
--     enabled=false row through the existing feature-gate admin surface.
--
-- Deliberately one key and one column, in its own migration, so it can be
-- reverted alone without touching the Projects schema.
--
-- Depends on: 20260824_01_cowork_gate_default_enabled.sql (default_enabled)
--             20260625_04_carl_featuregate_keys.sql (the ENABLE_RAG key row)

BEGIN;

-- Asserted rather than assumed. An UPDATE matching zero rows is not an error:
-- it would still be recorded as applied in public.hive_schema_migrations, the
-- gate would stay closed, Projects would stay unusable on every workspace with
-- no explicit row, and nothing anywhere would say so. Failing the migration is
-- the loud version of that.
--
-- The row's presence is what is asserted, not its spelling. feature_gate_keys.key
-- is the tenant_setting_key enum, so a typo would fail on the cast rather than
-- match nothing; what varies between installations is which registry rows are
-- seeded (20260715_04_featuregate_dynamic_keys.sql made the registry a table
-- rows are added to), and this file is worthless on one where the ENABLE_RAG row
-- is absent.
DO $$
DECLARE
    updated int;
BEGIN
    UPDATE public.feature_gate_keys
       SET default_enabled = true
     WHERE key = 'ENABLE_RAG';
    GET DIAGNOSTICS updated = ROW_COUNT;
    IF updated <> 1 THEN
        RAISE EXCEPTION
            'ENABLE_RAG registry row not found (matched % rows): the gate default was not applied', updated;
    END IF;
END$$;

COMMIT;
