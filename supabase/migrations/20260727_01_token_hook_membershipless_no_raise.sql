-- supabase/migrations/20260727_01_token_hook_membershipless_no_raise.sql
--
-- Fixes: every password grant for a user with no ACTIVE public.tenant_users
-- row returned HTTP 500 with {"code":"P0001","message":"no_active_membership"}.
-- Sign-up succeeded and first sign-in then failed, permanently, for that user.
--
-- Why those users exist at all: public.tenant_users is populated by
-- apps/control-plane/internal/signup (POST /internal/auth/user-created),
-- which was designed to be driven by a Supabase Database Webhook on
-- auth.users insert. That webhook is a dashboard-side setting, not repo
-- state, and on the live project it was never created, so no signup ever
-- produced a membership. Eleven of nineteen users were stranded and the
-- auth logs carried 92 occurrences of the exception in 24 hours.
--
-- The previous body (supabase/migrations/20260726_01_owui_role_claim.sql,
-- and originally 20260516_07_phase19_custom_access_token_hook.sql) chose to
-- RAISE rather than mint a token with a NULL tenant_id claim, on the
-- reasoning that downstream callers would otherwise run against a
-- partially-bound principal. The goal was right and is preserved here; the
-- mechanism was wrong. Aborting token issuance denies the user the one
-- request they need to make, which is the request that provisions their
-- membership, so the state is not recoverable from the client. It also
-- surfaces as a raw 500 carrying the database function's URI.
--
-- New behaviour for a membership-less user: issue a normal token and add no
-- tenant claims at all. tenant_id, tenants, role and owui_role are simply
-- absent. Absent is the correct encoding of "no tenant", and it is already
-- what every consumer fails closed on:
--
--   * apps/edge-api/internal/auth/middleware.go rejects a parsed principal
--     whose TenantID is uuid.Nil with 401 UNAUTHENTICATED before any handler
--     runs, and that middleware is the only writer of the request principal.
--   * RLS policies across public.tenant_users, public.tenant_settings and
--     the audit tables test (auth.jwt() ->> 'role') IN ('OWNER','ADMIN').
--     With no override emitted, 'role' keeps GoTrue's own value
--     ('authenticated'), which satisfies neither branch, so those policies
--     deny. Leaving 'role' alone is also the correct value for PostgREST's
--     role switching.
--
-- The member path is unchanged. For any user with at least one ACTIVE
-- membership on a non-archived tenant, the snapshot CTE, the
-- selected_tenant_id validation, the deterministic fallback ordering and all
-- four emitted claims (including the owui_role OWNER -> ADMIN remap that
-- Open WebUI's OAUTH_ROLES_CLAIM reads) are byte-for-byte identical to
-- 20260726_01_owui_role_claim.sql.

BEGIN;

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
    -- role lookup pinned to the same snapshot -- guarantees the role
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

  -- Zero-membership users (brand-new signup whose membership has not been
  -- reconciled yet, or every membership archived/revoked) get a valid token
  -- with NO tenant claims. Not a NULL tenant_id claim -- absent, so that a
  -- consumer reading the claim cannot mistake an explicit null for a
  -- wildcard. This token is inert by design: it authenticates the user to
  -- the console's provisioning call and nothing else. Do not restore the
  -- RAISE here; it made first sign-in unrecoverable for exactly the users
  -- who still need provisioning.
  IF selected IS NULL THEN
    RETURN jsonb_build_object('claims', claims);
  END IF;

  claims := claims
    || jsonb_build_object('tenant_id', selected)
    || jsonb_build_object('tenants',   COALESCE(tenant_list, '[]'::jsonb))
    || jsonb_build_object('role',      user_role)
    -- owui_role: Open WebUI's OAUTH_ALLOWED_ROLES vocabulary
    -- (ADMIN,MEMBER,VIEWER) has no OWNER entry, and it reads whichever
    -- claim OAUTH_ROLES_CLAIM points at. This claim exists solely for
    -- that integration; it must never be read by RLS policies or Go code
    -- in place of 'role'.
    || jsonb_build_object('owui_role', CASE WHEN user_role = 'OWNER' THEN 'ADMIN' ELSE user_role END);

  RETURN jsonb_build_object('claims', claims);
END;
$$;

REVOKE EXECUTE ON FUNCTION public.custom_access_token_hook FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.custom_access_token_hook TO supabase_auth_admin;

COMMIT;
