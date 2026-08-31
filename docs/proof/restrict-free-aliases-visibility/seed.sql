-- Proof fixture for the hive-free / hive-free-tools visibility restriction.
--
-- Applied on top of the real migration chain (supabase/migrations/*.sql,
-- which by this point includes 20260831_01_restrict_free_pool_aliases_
-- visibility.sql) in a scratch Postgres. It creates zero rows in
-- model_aliases: hive-free and hive-free-tools are real, migration-seeded
-- catalog rows, not synthetic proof fixtures. It only adds the two tenants.
--
-- Tenants:
--   customer   (c1111...) zero tenant_model_visibility rows. This is any
--              ordinary Hive tenant today: nothing in this repo grants
--              anyone visibility on a restricted alias by default.
--   automation (a1111...) visible=true grants on both aliases, mirroring
--              exactly what scripts/ci-seed-api-key.sh now inserts for its
--              own throwaway tenant on every CI run.

BEGIN;

INSERT INTO public.tenants (id, slug, name, deployment) VALUES
  ('c1111111-1111-1111-1111-111111111111', 'proof-customer',   'Proof Ordinary Customer Tenant', 'HIVE_CLOUD'),
  ('a1111111-1111-1111-1111-111111111111', 'proof-automation', 'Proof CI-Style Automation Tenant', 'HIVE_CLOUD')
ON CONFLICT (id) DO NOTHING;

INSERT INTO public.tenant_model_visibility (tenant_id, alias_id, visible) VALUES
  ('a1111111-1111-1111-1111-111111111111', 'hive-free',       true),
  ('a1111111-1111-1111-1111-111111111111', 'hive-free-tools', true)
ON CONFLICT (tenant_id, alias_id) DO UPDATE SET visible = EXCLUDED.visible;

COMMIT;

SELECT t.slug, v.alias_id, v.visible
FROM public.tenants t
LEFT JOIN public.tenant_model_visibility v ON v.tenant_id = t.id
WHERE t.slug LIKE 'proof-%'
ORDER BY t.slug, v.alias_id;
