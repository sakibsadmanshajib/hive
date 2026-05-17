-- supabase/migrations/20260516_07_phase19_custom_access_token_hook.sql
-- Phase 19 — Supabase Auth custom-access-token hook. Injects
-- tenant_id, tenants[], and role into every issued access token.

BEGIN;

-- The hook reads public.tenant_users / public.tenants, which are RLS-
-- protected and key off auth.jwt()->>'tenant_id'. During token issuance
-- the JWT claims do not yet exist, so RLS would deny the lookup and the
-- function would silently return empty memberships. SECURITY DEFINER
-- runs the function with the owner's privileges so the RLS predicates
-- are bypassed; SET search_path = '' is the standard guard against
-- privilege escalation via mutable schema resolution.
CREATE OR REPLACE FUNCTION public.custom_access_token_hook(event jsonb)
RETURNS jsonb
LANGUAGE plpgsql STABLE
SECURITY DEFINER
SET search_path = ''
AS $$
DECLARE
  claims          jsonb;
  uid             uuid;
  tenant_list     jsonb;
  selected        uuid;
  selected_raw    text;
  user_role       text;
  -- RFC-4122 UUID format. Used to validate user-mutable
  -- raw_user_meta_data.selected_tenant_id before casting, so a
  -- malformed value cannot raise 22P02 and block token issuance.
  uuid_regex CONSTANT text :=
    '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$';
BEGIN
  uid := (event->>'user_id')::uuid;
  claims := event->'claims';

  -- Deterministic ordering on the membership list so the fallback
  -- (tenant_list->0) is stable across token issuances for users with
  -- multiple active memberships.
  SELECT jsonb_agg(
           jsonb_build_object('id', t.id, 'role', tu.role)
           ORDER BY tu.joined_at ASC, t.id ASC
         )
    INTO tenant_list
    FROM public.tenant_users tu
    JOIN public.tenants t ON t.id = tu.tenant_id
   WHERE tu.user_id = uid
     AND tu.status  = 'ACTIVE'
     AND t.archived_at IS NULL;

  -- raw_user_meta_data is user-mutable, so it cannot be trusted as the
  -- source of the tenant_id authorization claim. Read as text, validate
  -- it parses as a uuid, then verify the user currently has an active
  -- membership for it; otherwise null it out and fall back to the
  -- first active tenant.
  SELECT raw_user_meta_data->>'selected_tenant_id'
    INTO selected_raw
    FROM auth.users
   WHERE id = uid;

  IF selected_raw IS NOT NULL AND selected_raw ~* uuid_regex THEN
    selected := selected_raw::uuid;
  ELSE
    selected := NULL;
  END IF;

  IF selected IS NOT NULL AND NOT EXISTS (
    SELECT 1
      FROM public.tenant_users tu
      JOIN public.tenants t ON t.id = tu.tenant_id
     WHERE tu.user_id   = uid
       AND tu.tenant_id = selected
       AND tu.status    = 'ACTIVE'
       AND t.archived_at IS NULL
  ) THEN
    selected := NULL;
  END IF;

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

REVOKE EXECUTE ON FUNCTION public.custom_access_token_hook FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.custom_access_token_hook TO supabase_auth_admin;

COMMIT;
