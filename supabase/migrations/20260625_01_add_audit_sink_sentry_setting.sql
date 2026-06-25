-- Idempotent migration: add ENABLE_AUDIT_SINK_SENTRY to the
-- public.tenant_setting_key enum. All existing sinks (ELK, Loki, Datadog,
-- Splunk, Langfuse) were added in 20260516_02. Sentry was wired in code but
-- the enum value was missing, which would cause a cast error if an operator
-- attempted to insert a tenant_settings row for the Sentry sink.
--
-- ALTER TYPE ... ADD VALUE is idempotent via the IF NOT EXISTS guard (Postgres 12+).
-- No DOWN migration: enum values cannot be removed in Postgres without a full
-- type rebuild, and the value is harmless when unused.

ALTER TYPE public.tenant_setting_key ADD VALUE IF NOT EXISTS 'ENABLE_AUDIT_SINK_SENTRY';
