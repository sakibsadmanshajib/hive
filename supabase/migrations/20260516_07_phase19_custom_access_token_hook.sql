-- supabase/migrations/20260516_07_phase19_custom_access_token_hook.sql
-- Phase 19 — Supabase Auth custom-access-token hook. Injects
-- tenant_id, tenants[], and role into every issued access token.

BEGIN;

CREATE OR REPLACE FUNCTION public.custom_access_token_hook(event jsonb)
RETURNS jsonb
LANGUAGE plpgsql STABLE
AS $$
DECLARE
  claims        jsonb;
  uid           uuid;
  tenant_list   jsonb;
  selected      uuid;
  user_role     text;
BEGIN
  uid := (event->>'user_id')::uuid;
  claims := event->'claims';

  SELECT jsonb_agg(jsonb_build_object('id', t.id, 'role', tu.role))
    INTO tenant_list
    FROM public.tenant_users tu
    JOIN public.tenants t ON t.id = tu.tenant_id
   WHERE tu.user_id = uid
     AND tu.status  = 'ACTIVE'
     AND t.archived_at IS NULL;

  SELECT (raw_user_meta_data->>'selected_tenant_id')::uuid
    INTO selected
    FROM auth.users
   WHERE id = uid;

  IF selected IS NULL AND tenant_list IS NOT NULL
     AND jsonb_array_length(tenant_list) > 0 THEN
    selected := (tenant_list->0->>'id')::uuid;
  END IF;

  SELECT role INTO user_role
    FROM public.tenant_users
   WHERE user_id = uid AND tenant_id = selected;

  claims := claims
    || jsonb_build_object('tenant_id', selected)
    || jsonb_build_object('tenants',   COALESCE(tenant_list, '[]'::jsonb))
    || jsonb_build_object('role',      user_role);

  RETURN jsonb_build_object('claims', claims);
END;
$$;

GRANT EXECUTE ON FUNCTION public.custom_access_token_hook TO supabase_auth_admin;

COMMIT;
