-- Correct the public.marketplace_entries table comment (issue #758, security
-- review of PR #788).
--
-- 20260716_01_marketplace_catalog.sql described this table as gated at the HTTP
-- layer by platform-admin. That is no longer the whole truth: the catalog read
-- and the per-tenant enablement are now gated on
-- apps/control-plane/internal/platform.WorkspaceAdminGate, which admits the
-- OWNER of the tenant on the session as well as a platform admin, while
-- curation stays platform-admin only. The next reader of the schema should not
-- be told the wrong gate.
--
-- Comment only. No table, policy, grant, or row is changed, and an already
-- applied 20260716_01 is left untouched.

COMMENT ON TABLE public.marketplace_entries IS
  'Admin-curated marketplace catalog: MCP servers, rules, skills, and prompt templates any tenant may enable (issue #309). config is kind-specific (an MCP server stores command/args/env or url/transport in the shape apps/agent-engine consumes via marketplaceclient). No RLS and no GRANT to authenticated: this is shared platform catalog data, not tenant data, read and written only through apps/control-plane/internal/marketplace. Curation (create, edit, delete) is gated at the HTTP layer on platform-admin. The catalog read and the per-tenant enablement are gated on platform.WorkspaceAdminGate, which also admits the OWNER of the tenant on the session (issue #758). The raw config is served only to a caller who may curate, because an MCP server entry can carry a credential in env.';
