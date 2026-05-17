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

  -- raw_user_meta_data is user-mutable, so it cannot be trusted as the
  -- source of the tenant_id authorization claim. Read as text, validate
  -- it parses as a uuid; existence + activeness is verified against the
  -- snapshot below.
  SELECT raw_user_meta_data->>'selected_tenant_id'
    INTO selected_raw
    FROM auth.users
   WHERE id = uid;

  IF selected_raw IS NOT NULL AND selected_raw ~* uuid_regex THEN
    selected := selected_raw::uuid;
  ELSE
    selected := NULL;
  END IF;

  -- Single snapshot of (membership × tenant) read once and reused for
  -- tenant_list, selected validation, and user_role. Splitting the
  -- read across three SELECTs in READ COMMITTED let a concurrent
  -- archive/revoke produce inconsistent claims (e.g. selected tenant
  -- still in tenants[] but role NULL); pulling them from one CTE
  -- materialised per-call closes that window.
  WITH active_memberships AS (
    SELECT tu.tenant_id, tu.role, tu.joined_at
      FROM public.tenant_users tu
      JOIN public.tenants t ON t.id = tu.tenant_id
     WHERE tu.user_id     = uid
       AND tu.status      = 'ACTIVE'
       AND t.archived_at IS NULL
  )
  SELECT
    -- Deterministic ordering so the fallback (tenant_list->0) is stable
    -- across token issuances for users with multiple active memberships.
    (SELECT jsonb_agg(
              jsonb_build_object('id', am.tenant_id, 'role', am.role)
              ORDER BY am.joined_at ASC, am.tenant_id ASC
            ) FROM active_memberships am),
    -- selected_tenant_id is only trusted when it appears in the snapshot.
    (SELECT am.tenant_id FROM active_memberships am
      WHERE am.tenant_id = selected LIMIT 1),
    -- role lookup pinned to the same snapshot — guarantees the role
    -- claim is sourced from a row that was still active at snapshot time.
    (SELECT am.role FROM active_memberships am
      WHERE am.tenant_id = selected LIMIT 1)
    INTO tenant_list, selected, user_role;

  -- Fallback: if the user-supplied selection didn't match an active
  -- membership, pin to the first active membership (deterministic order)
  -- and re-derive the role from the same snapshot.
  IF selected IS NULL AND tenant_list IS NOT NULL
     AND jsonb_array_length(tenant_list) > 0 THEN
    selected  := (tenant_list->0->>'id')::uuid;
    user_role := tenant_list->0->>'role';
  END IF;

  -- Zero-membership users (brand-new signup before the webhook fires,
  -- or every membership archived/revoked) must NOT receive a token
  -- with a NULL tenant_id claim — downstream callers would then run
  -- against a partially-bound principal. Abort issuance instead; the
  -- caller sees the standard Supabase Auth error and can retry once
  -- provisioning completes.
  IF selected IS NULL THEN
    RAISE EXCEPTION 'no_active_membership';
  END IF;

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
