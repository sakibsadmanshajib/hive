-- Issue #1507: agent tasks charged their tenant nothing, because every sandbox
-- spent one Hive-owned API key and the gateway settled that key's own account.
-- The fix mints one short-lived API key per agent task on the task's own tenant
-- billing account, which means public.api_keys now carries rows a customer
-- never created and cannot use.
--
-- Those rows must not appear in the customer's API Keys list. This column is
-- the structural discriminator that keeps them out.
--
-- Why a column and not a name prefix. The obvious no-migration filter is
-- "hide keys whose nickname starts with 'agent task '", and it fails twice, both
-- times silently: a customer who names their own key "agent task backfill" has
-- it hidden from their own list with no error anywhere, and the filter stops
-- working the moment anyone rewords the label, again with no error anywhere.
-- Neither failure is caught by a test written against today's naming. A display
-- string is not an identity.
--
-- Why not join to public.agent_tasks on the shared id instead (the per-task key
-- deliberately uses the agent task's own id as its primary key, so the
-- relationship already exists). Rejected on purpose: control-plane connects as
-- hive_app, which is NOT BYPASSRLS, and public.agent_tasks is RLS-protected
-- behind app.current_tenant_id. A NOT EXISTS subquery against it from a
-- connection with no tenant GUC set matches nothing, so the filter would
-- silently pass every agent-task key straight through to the customer's list.
-- A filter that fails open under a condition nobody tests is worse than no
-- filter, because it looks present in code review.

BEGIN;

ALTER TABLE public.api_keys
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'user'
        CHECK (kind IN ('user', 'agent_task'));

COMMENT ON COLUMN public.api_keys.kind IS
  'What minted this key and who it is for. ''user'' is a customer-created key '
  'and is the only kind the console lists; ''agent_task'' is one short-lived '
  'credential minted per agent task so the sandbox''s inference settles '
  'against the tenant that submitted it (issue #1507), and its id is that '
  'task''s own id. The DEFAULT makes every pre-existing row ''user'', which is '
  'correct: no agent-task key existed before this migration. Filtering happens '
  'in apikeys'' repository ListKeys, not in a handler, so no caller can forget '
  'it.';

-- The list query is (account_id, kind) filtered, created_at ordered, and it is
-- the page a customer loads on every visit to the console's API Keys screen.
-- Partial on the listed kind so the index stays the size of the customer's own
-- keys rather than growing with one row per agent task forever.
CREATE INDEX IF NOT EXISTS api_keys_account_user_kind_created_idx
    ON public.api_keys (account_id, created_at DESC)
    WHERE kind = 'user';

COMMIT;
