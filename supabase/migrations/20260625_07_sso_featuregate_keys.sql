-- Add SSO provider feature gate keys to the tenant_setting_key enum.
-- These keys enable per-tenant SSO via GoTrue native SAML 2.0 and OIDC
-- (issue #237). All three values are added by this migration.
-- ALTER TYPE ... ADD VALUE IF NOT EXISTS is idempotent (Postgres 12+) so
-- re-running this migration is safe.
--
-- No DOWN migration: Postgres enum values cannot be removed without a full
-- type rebuild; unused values are harmless.

ALTER TYPE public.tenant_setting_key ADD VALUE IF NOT EXISTS 'ENABLE_SSO_GOOGLE';
ALTER TYPE public.tenant_setting_key ADD VALUE IF NOT EXISTS 'ENABLE_SSO_MICROSOFT';
ALTER TYPE public.tenant_setting_key ADD VALUE IF NOT EXISTS 'ENABLE_SSO_SAML';
