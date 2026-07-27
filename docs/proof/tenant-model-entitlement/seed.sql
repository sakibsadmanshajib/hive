-- Proof fixture for per-tenant model entitlement on the inference path.
--
-- Applied on top of the real migration chain (supabase/migrations/*.sql) in a
-- scratch Postgres. It creates three tenants with distinct visibility postures
-- and one restricted alias, so a single alias can be shown allowed for one
-- tenant and refused for another on the same running control-plane.
--
-- Tenants:
--   entitled  (1111...) no visibility rows for hive-fast  -> allowed
--   blocked   (2222...) hive-fast visible=false           -> refused
--   greenfield(3333...) zero visibility rows at all       -> allowed (the
--                       production-safety case: tenant_model_visibility ships
--                       empty on every deployment)
--
-- Restricted alias hive-restricted-proof: entitled holds a visible=true grant,
-- greenfield holds nothing, so the same alias is allowed for one and refused for
-- the other purely on the grant.

BEGIN;

INSERT INTO public.tenants (id, slug, name, deployment) VALUES
  ('11111111-1111-1111-1111-111111111111', 'proof-entitled',   'Proof Entitled Tenant',   'ENTERPRISE_EDGE'),
  ('22222222-2222-2222-2222-222222222222', 'proof-blocked',    'Proof Blocked Tenant',    'ENTERPRISE_EDGE'),
  ('33333333-3333-3333-3333-333333333333', 'proof-greenfield', 'Proof Greenfield Tenant', 'ENTERPRISE_EDGE')
ON CONFLICT (id) DO NOTHING;

-- A restricted alias plus one healthy chat route, mirroring the seeded aliases.
INSERT INTO public.model_aliases (
  alias_id, owned_by, display_name, summary, visibility, lifecycle,
  capability_badges, input_price_credits, output_price_credits
) VALUES (
  'hive-restricted-proof', 'hive', 'Hive Restricted (proof)',
  'Restricted alias used to prove grant-only entitlement.', 'restricted', 'stable',
  '["chat"]'::jsonb, 10, 30
) ON CONFLICT (alias_id) DO NOTHING;

INSERT INTO public.provider_routes (
  route_id, alias_id, provider, provider_model, litellm_model_name,
  price_class, health_state, priority
) VALUES (
  'route-proof-restricted', 'hive-restricted-proof', 'groq',
  'groq/llama-3.1-8b-instant', 'route-proof-restricted', 'standard', 'healthy', 10
) ON CONFLICT (route_id) DO NOTHING;

INSERT INTO public.provider_capabilities (
  route_id, supports_responses, supports_chat_completions, supports_streaming
) VALUES ('route-proof-restricted', true, true, true)
ON CONFLICT (route_id) DO NOTHING;

INSERT INTO public.alias_route_policies (alias_id, policy_mode, allow_price_class_widening, fallback_order)
VALUES ('hive-restricted-proof', 'latency', false, '["route-proof-restricted"]'::jsonb)
ON CONFLICT (alias_id) DO NOTHING;

-- Visibility rows. Note what is deliberately absent: the entitled tenant has no
-- row for hive-fast (public aliases stay allowed with no row) and the greenfield
-- tenant has no rows at all.
INSERT INTO public.tenant_model_visibility (tenant_id, alias_id, visible) VALUES
  ('22222222-2222-2222-2222-222222222222', 'hive-fast',             false),
  ('11111111-1111-1111-1111-111111111111', 'hive-restricted-proof', true)
ON CONFLICT (tenant_id, alias_id) DO UPDATE SET visible = EXCLUDED.visible;

COMMIT;

SELECT t.slug, v.alias_id, v.visible
FROM public.tenants t
LEFT JOIN public.tenant_model_visibility v ON v.tenant_id = t.id
WHERE t.slug LIKE 'proof-%'
ORDER BY t.slug, v.alias_id;
