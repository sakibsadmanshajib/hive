-- Retire the six audit sink feature gates (issue #755).
--
-- What was wrong
-- --------------
-- The console rendered six switches, `ENABLE_AUDIT_SINK_{ELK,LOKI,DATADOG,
-- SPLUNK,SENTRY,LANGFUSE}`, under category `audit_sink`. Flipping one wrote a
-- row to public.tenant_settings and nothing read it. The real decision was
-- made once at process start from the process environment, in
-- apps/control-plane/cmd/server/main.go. An operator could enable audit export
-- in the console, see it persist, and change nothing. On a product sold on
-- auditability that is a compliance integrity defect: a control that reports a
-- state it does not have is worse than an absent control.
--
-- Issue #755 allowed exactly two resolutions: make the toggle drive real
-- behaviour, or take it off the rendered surface. This migration is the second,
-- and the reasons are in decreasing order of weight.
--
-- 1. It would be an audit-evasion control. `audit_sink` is not in
--    featuregate.platformManagedCategories, so a workspace OWNER can flip
--    these rows. Wiring them for real would hand a tenant the ability to
--    suppress export of its own audit trail to the operator's SIEM. That is
--    not a feature with a security caveat. This argument stands on its own,
--    independent of everything below.
--
-- 2. The scopes do not match. tenant_settings is keyed (tenant_id, key). The
--    sink set is process-global: one worker, one credential set, and
--    public.audit_outbox has no tenant_id column to resolve a per-tenant
--    decision against. Under Hive Enterprise (D-007, single org equals single
--    tenant) the per-tenant switch is degenerate anyway.
--
-- 3. Splitting one control across two stores is the failure mode D-044 was
--    written about. Credentials stay in a reviewed, version-controlled .env;
--    moving the enable half into an unversioned runtime row would reproduce
--    exactly the "unreviewed, unversioned, unreproducible in a fresh
--    environment, silently revertible by a volume reset" shape D-044 names.
--
-- 4. Nothing enqueues public.audit_outbox in production. Every INSERT in the
--    repository is in a test, and there is no trigger on audit_log. No sink
--    exports anything today regardless of configuration, so this removal takes
--    away nothing that worked. Tracked separately; not fixed here.
--
-- What replaces it
-- ----------------
-- Nothing in the console. Audit sink enablement is deployment configuration:
-- an operator sets ENABLE_AUDIT_SINK_* to true in the deployment environment
-- alongside that sink's credentials, both of which are documented in
-- .env.example. Neither half alone enables a sink, which is the zero-egress
-- posture and is unchanged by this migration.
--
-- Why these six and not the other sixteen inert keys
-- --------------------------------------------------
-- Twenty two of the twenty five registry rows have no runtime reader
-- (20260829_02_feature_gate_enforcement_site.sql). Sixteen of them stay,
-- rendered with the "not enforced yet" disclosure that issue #762 shipped.
-- These six are different on two counts that none of the sixteen share: they
-- are the only inert keys whose enablement would start outbound egress to a
-- third party, and the only ones a tenant could flip to suppress the
-- operator's own audit export. The remaining sixteen are tracked on their own
-- issue and need their own decision, not this one applied by analogy.
--
-- Mechanics
-- ---------
-- Deleting the registry rows is the whole removal. settings.Resolver.Registry
-- reads feature_gate_keys, so the console renders nothing; settings.Resolver.Set
-- checks feature_gate_keys before writing, so the PUT path now answers
-- ErrUnknownGateKey, which the admin handler maps to 400. There is no foreign
-- key from tenant_settings to feature_gate_keys, so the stored rows have to be
-- deleted explicitly or they linger as state that reports a control; that is
-- the first statement, and it runs first for the same reason.
--
-- The public.tenant_setting_key enum labels are deliberately left in place.
-- Dropping an enum label is not supported without recreating the type and
-- rewriting every column that uses it, and the labels are inert once no
-- registry row and no stored row references them.
--
-- No DOWN migration, matching 20260715_04. Restoring these rows would restore
-- the defect.
--
-- Depends on: 20260715_04_featuregate_dynamic_keys.sql (public.feature_gate_keys),
--             20260516_02_phase19_tenant_settings.sql (public.tenant_settings)

BEGIN;

DELETE FROM public.tenant_settings
 WHERE key IN (
   'ENABLE_AUDIT_SINK_ELK',
   'ENABLE_AUDIT_SINK_LOKI',
   'ENABLE_AUDIT_SINK_DATADOG',
   'ENABLE_AUDIT_SINK_SPLUNK',
   'ENABLE_AUDIT_SINK_SENTRY',
   'ENABLE_AUDIT_SINK_LANGFUSE'
 );

DELETE FROM public.feature_gate_keys
 WHERE key IN (
   'ENABLE_AUDIT_SINK_ELK',
   'ENABLE_AUDIT_SINK_LOKI',
   'ENABLE_AUDIT_SINK_DATADOG',
   'ENABLE_AUDIT_SINK_SPLUNK',
   'ENABLE_AUDIT_SINK_SENTRY',
   'ENABLE_AUDIT_SINK_LANGFUSE'
 );

COMMIT;
