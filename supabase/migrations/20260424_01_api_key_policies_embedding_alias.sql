-- Extend existing API-key policies to include the newly-seeded
-- `hive-embedding-default` alias (introduced in
-- 20260423_01_embedding_alias.sql).
--
-- Without this, any API key whose policy explicitly lists a subset of
-- aliases (e.g. ["hive-default","hive-fast","hive-auto"]) will be denied
-- access to the embedding alias and the edge-api authz layer returns
-- `404 The model 'hive-embedding-default' does not exist or you do not
-- have access to it.` — even though the alias exists in the catalog.
--
-- Scope: only policies that already grant access to `hive-default`. We
-- do not widen access for keys that were intentionally restricted to
-- zero aliases (`allowed_aliases = []`). Policies with `allow_all_models
-- = true` are unaffected because CheckAccess already short-circuits.

update public.api_key_policies
   set allowed_aliases = allowed_aliases || '"hive-embedding-default"'::jsonb
 where allowed_aliases @> '["hive-default"]'::jsonb
   and not (allowed_aliases @> '["hive-embedding-default"]'::jsonb);
