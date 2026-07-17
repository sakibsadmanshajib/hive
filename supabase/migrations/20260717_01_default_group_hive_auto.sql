-- Add `hive-auto` to the `default` model policy group.
--
-- 20260331_03_api_key_policies.sql seeded `hive-auto` into the `premium` and
-- `closed` groups only. api_key_policies.allowed_group_names defaults to
-- ["default"], so any key on the default tier (including the demo's default
-- API key) resolves its alias list from the `default` group alone and never
-- sees `hive-auto` -- edge-api's authz layer then returns
-- `404 The model 'hive-auto' does not exist or you do not have access to it`
-- for any vision (or other hive-auto-routed) request from a default-tier
-- key, even though the alias exists in the catalog.
--
-- Mirrors the existing `default` membership rows for hive-default/hive-fast;
-- ON CONFLICT DO NOTHING keeps this safe to re-run.

insert into public.model_policy_group_members (group_name, alias_id)
values ('default', 'hive-auto')
on conflict (group_name, alias_id) do nothing;
