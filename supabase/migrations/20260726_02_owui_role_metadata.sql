-- supabase/migrations/20260726_02_owui_role_metadata.sql
-- #457: every self-serve tenant's second-and-later Open WebUI OAuth login
-- (not the very first user Open WebUI itself ever sees) landed on Account
-- Activation Pending forever, even after PR #451's owui_role access-token
-- claim fix (supabase/migrations/20260726_01_owui_role_claim.sql).
--
-- Root cause, confirmed live (nightly e2e DEBUG logs, run 30219482725):
-- Supabase's OAuth Authorization Server issues a separate id_token for
-- third-party OIDC clients like Open WebUI that never runs through
-- custom_access_token_hook -- that hook only fires on GoTrue's own
-- session/access-token issuance, not this third-party OAuth-provider token
-- path. So Open WebUI's OAUTH_ROLES_CLAIM lookup for 'owui_role' always saw
-- an empty claim list ("User roles from oauth: []"), silently falling
-- through to DEFAULT_USER_ROLE for every user except Open WebUI's own
-- unconditional first-ever-user-becomes-admin bypass (which is what let PR
-- #451 look correct despite this gap).
--
-- user_metadata IS serialized onto every token GoTrue issues, in every
-- flow, hook or no hook (base GoTrue claims struct) -- so mirror
-- owui_role there instead of relying on the access-token-only hook, kept
-- in sync by a trigger on tenant_users. deploy/docker/docker-compose.yml's
-- OAUTH_ROLES_CLAIM is repointed at 'user_metadata.owui_role' in the same
-- change.
--
-- Known limitation: this trigger reflects whichever tenant_users row was
-- last inserted/updated, not necessarily the user's currently "selected"
-- tenant (see raw_user_meta_data.selected_tenant_id, resolved properly by
-- custom_access_token_hook for the 'role'/'tenant_id' claims). Multi-tenant
-- users who switch tenants and re-log into Open WebUI may see a stale
-- owui_role until their next tenant_users row change. Out of scope here:
-- the incident and its e2e coverage are both single-membership self-serve
-- OWNER access, not tenant switching.

BEGIN;

CREATE OR REPLACE FUNCTION public.sync_owui_role_metadata()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
DECLARE
  target_user_id uuid := COALESCE(NEW.user_id, OLD.user_id);
  computed_role  text;
BEGIN
  IF TG_OP = 'DELETE' OR NEW.status <> 'ACTIVE' THEN
    computed_role := 'pending';
  ELSE
    -- Open WebUI's OAUTH_ALLOWED_ROLES vocabulary (ADMIN,MEMBER,VIEWER)
    -- has no OWNER entry; map it the same way the owui_role access-token
    -- claim already does.
    computed_role := CASE WHEN NEW.role = 'OWNER' THEN 'ADMIN' ELSE NEW.role END;
  END IF;

  UPDATE auth.users
     SET raw_user_meta_data = COALESCE(raw_user_meta_data, '{}'::jsonb)
                              || jsonb_build_object('owui_role', computed_role)
   WHERE id = target_user_id;

  RETURN NULL;
END;
$$;

-- Defense in depth, matching custom_access_token_hook's own posture: this
-- is a trigger function, never called directly over RPC, but pin the
-- grant anyway so nothing can invoke it standalone as PUBLIC/authenticated.
REVOKE EXECUTE ON FUNCTION public.sync_owui_role_metadata FROM PUBLIC;

DROP TRIGGER IF EXISTS tenant_users_sync_owui_role ON public.tenant_users;
CREATE TRIGGER tenant_users_sync_owui_role
AFTER INSERT OR UPDATE OF role, status OR DELETE ON public.tenant_users
FOR EACH ROW EXECUTE FUNCTION public.sync_owui_role_metadata();

-- Backfill: existing active memberships never got a chance to fire the
-- trigger above. Pick each user's most-recently-joined active membership
-- (deterministic, mirrors custom_access_token_hook's own fallback
-- ordering) so a currently-broken user is fixed by this migration alone,
-- without waiting on their next tenant_users write.
UPDATE auth.users u
   SET raw_user_meta_data = COALESCE(u.raw_user_meta_data, '{}'::jsonb)
                            || jsonb_build_object(
                                 'owui_role',
                                 CASE WHEN latest.role = 'OWNER' THEN 'ADMIN' ELSE latest.role END
                               )
  FROM (
    SELECT DISTINCT ON (tu.user_id) tu.user_id, tu.role
      FROM public.tenant_users tu
     WHERE tu.status = 'ACTIVE'
     ORDER BY tu.user_id, tu.joined_at DESC
  ) latest
 WHERE u.id = latest.user_id;

COMMIT;
