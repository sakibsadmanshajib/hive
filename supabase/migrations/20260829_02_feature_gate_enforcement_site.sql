-- Record where each registered feature gate is actually read at runtime
-- (issue #762, rule 1).
--
-- Twenty two of the twenty five rows in public.feature_gate_keys have no
-- runtime reader at all. They persist to public.tenant_settings correctly and
-- nothing ever looks at the value: six audit sink toggles are decided from
-- process environment variables instead (#755), seven billing gates including
-- both payment rails have no consumer (#756), three SSO gates have an
-- enforcement helper that is never mounted on a route (#757), and three admin
-- keys, two RAG keys and ENABLE_RELAY are in the same shape (#758).
--
-- Until now that was invisible in the product because the admin page these
-- rows render on could not load at all: every caller reached it with
-- Viewer.TenantID = uuid.Nil and platform.WorkspaceAdminGate answered 400. That
-- is fixed in the same change as this migration, which turns a dormant problem
-- into a live one: an operator who can now reach the page would be shown
-- twenty two switches, be told changes "take effect across the API and apps",
-- and change nothing. A control that lies about what it controls is worse than
-- no control, most of all on a product sold on auditability.
--
-- The honest fix available in one change is to say so on the row. Wiring the
-- twenty two readers is not one change: #762 states that the first move on each
-- of #755 to #758 is a decision rather than an implementation, and each carries
-- an open design question (whether audit sinks are deployment scoped or tenant
-- scoped, whether ENABLE_PER_USER_CAP should exist at all given no per-user cap
-- was ever built, whether Open WebUI's OAuth flow can be gated at all when it
-- does not route through edge-api middleware).
--
-- Why a column here rather than a list in the console: adding a gate key is a
-- migration-only change by design, so a frontend list would go stale the first
-- time a migration added a key, and silently. Putting the fact next to the key
-- means the same statement that publishes a control also states whether
-- anything enforces it.
--
-- Free text, not an enum or a foreign key. The value is read by a human
-- deciding whether a gate is real; the wire only carries whether it is blank.
-- Pinning it to a symbol would go stale against the file it names without any
-- of the benefit.
--
-- Depends on: 20260715_04_featuregate_dynamic_keys.sql (public.feature_gate_keys)

BEGIN;

ALTER TABLE public.feature_gate_keys
  ADD COLUMN IF NOT EXISTS enforcement_site text;

COMMENT ON COLUMN public.feature_gate_keys.enforcement_site IS
  'Where this key is read at runtime, as a human-readable location. NULL means nothing reads it: the value persists and changes no behaviour, and the console renders the row as stored but not enforced. Fill this in the same change that mounts the reader, never before.';

-- The three that are genuinely mounted. Every other row stays NULL, which is
-- the column default, so no UPDATE is needed for them and a key added by a
-- later migration is unenforced until someone says otherwise.
UPDATE public.feature_gate_keys
   SET enforcement_site = 'edge-api: /v1/rag/ via gate.Require(FeatureRAG), apps/edge-api/cmd/server/gated_routes.go'
 WHERE key = 'ENABLE_RAG';

UPDATE public.feature_gate_keys
   SET enforcement_site = 'edge-api: /v1/audio/* via voiceGateForAPIKeys(gate.Require(FeatureVoice)), apps/edge-api/cmd/server/main.go'
 WHERE key = 'ENABLE_VOICE';

UPDATE public.feature_gate_keys
   SET enforcement_site = 'edge-api: agent task and schedule routes via gate.Require(FeatureCowork), apps/edge-api/cmd/server/gated_routes.go'
 WHERE key = 'ENABLE_COWORK';

COMMIT;
