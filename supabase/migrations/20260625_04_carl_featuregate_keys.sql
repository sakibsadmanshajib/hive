-- Add Carl.sh sovereign workspace feature gate keys to the tenant_setting_key enum.
-- ALTER TYPE ... ADD VALUE IF NOT EXISTS is idempotent (Postgres 12+).
-- No DOWN migration: enum values cannot be removed in Postgres without a full
-- type rebuild, and the values are harmless when unused.

ALTER TYPE public.tenant_setting_key ADD VALUE IF NOT EXISTS 'ENABLE_RAG';
ALTER TYPE public.tenant_setting_key ADD VALUE IF NOT EXISTS 'ENABLE_VOICE';
ALTER TYPE public.tenant_setting_key ADD VALUE IF NOT EXISTS 'ENABLE_RELAY';
ALTER TYPE public.tenant_setting_key ADD VALUE IF NOT EXISTS 'ENABLE_COWORK';
