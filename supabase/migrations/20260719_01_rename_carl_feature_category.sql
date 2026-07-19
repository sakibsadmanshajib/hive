-- Rename the retired "carl" feature-gate category to "agents" (owner
-- decision: the Carl.sh product name is retired repo-wide; this is the last
-- client-visible surface still carrying it).
--
-- public.feature_gate_keys.category = 'carl' groups ENABLE_RAG, ENABLE_VOICE,
-- ENABLE_RELAY, ENABLE_COWORK (see 20260715_04_featuregate_dynamic_keys.sql).
-- These are the agent-subsystem capability keys (RAG, voice, relay, cowork),
-- so "agents" replaces "carl" as the category value. The per-row labels also
-- carried the retired product name ("Carl.sh <x> capability") and are
-- rewritten to match ("Agent <x> capability").
--
-- apps/control-plane/internal/tenant/settings/resolver.go's
-- clientVisibleGateCategories allowlist is updated in the same PR to read
-- "agents" instead of "carl"; this migration and that code change must ship
-- together; a rollback of one requires reverting the other before the client
-- endpoint (control-plane featuregate handler, edge-api /v1/featuregate) sees
-- these keys reappear.
--
-- Breaking change: any consumer keyed off category="carl" (admin console
-- category grouping, future third-party integrations) must be updated. No
-- API shape change: keys, labels' meaning, and enabled state are unaffected,
-- only the category string and label text.
--
-- Idempotent: both UPDATEs are no-ops once applied (WHERE clauses match
-- nothing on a second run).

BEGIN;

UPDATE public.feature_gate_keys
   SET category = 'agents'
 WHERE category = 'carl';

UPDATE public.feature_gate_keys
   SET label = 'Agent RAG capability'
 WHERE key = 'ENABLE_RAG' AND label = 'Carl.sh RAG capability';

UPDATE public.feature_gate_keys
   SET label = 'Agent voice capability'
 WHERE key = 'ENABLE_VOICE' AND label = 'Carl.sh voice capability';

UPDATE public.feature_gate_keys
   SET label = 'Agent relay capability'
 WHERE key = 'ENABLE_RELAY' AND label = 'Carl.sh relay capability';

UPDATE public.feature_gate_keys
   SET label = 'Agent cowork capability'
 WHERE key = 'ENABLE_COWORK' AND label = 'Carl.sh cowork capability';

COMMIT;
