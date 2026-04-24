-- Attach the `hive-embedding-default` alias to the launch-safe model policy
-- groups so any API key whose policy references `default` (or `closed`)
-- resolves the embedding alias through its group membership.
--
-- Without this, ResolveSnapshot in apps/control-plane/internal/apikeys/service.go
-- builds AuthSnapshot.AllowedAliases from group_members + explicit
-- allowed_aliases, and CI keys subscribed only to `default` (which
-- currently holds `[hive-default, hive-fast]`) see a 404
-- `model_not_allowed` at the authz layer for /v1/embeddings calls.

insert into public.model_policy_group_members (group_name, alias_id) values
  ('default', 'hive-embedding-default'),
  ('closed',  'hive-embedding-default')
on conflict (group_name, alias_id) do nothing;
