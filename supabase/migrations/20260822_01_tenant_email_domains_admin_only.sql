-- supabase/migrations/20260822_01_tenant_email_domains_admin_only.sql
-- Email-domain claims become an administrator operation, not a self-service one.
--
-- WHY, and it is a tenancy-isolation hole rather than tidying. Since Phase 19,
-- public.tenant_email_domains has granted INSERT and DELETE to `authenticated`,
-- and its FOR ALL policy checks only `tenant_id = auth.jwt() ->> 'tenant_id'`.
-- A FOR ALL policy with only USING and no WITH CHECK applies that same
-- expression as WITH CHECK, so the policy does constrain WHICH tenant a row can
-- be attached to: your own. What it does not constrain at all is WHICH DOMAIN,
-- and `domain` is the primary key, so claims are first come first served.
--
-- Every user owns a personal tenant and is therefore `authenticated` with a
-- tenant_id claim. So any signed-in user could insert ('gmail.com', <their own
-- tenant>) through the data API and, from that moment, every new identity
-- signing up with a gmail.com address resolves to their tenant and is given a
-- membership in it. They then hold tenant-owner visibility over strangers.
--
-- That was reachable but inert while provisioning ran through a Supabase
-- dashboard webhook that had been deleted. PR #993 made the control-plane sweep
-- for membership-less identities and provision them itself, which makes domain
-- auto-attachment happen automatically, on a timer, with no human in the loop.
-- Closing the write surface is the correct half of that change (review finding
-- on PR 993).
--
-- The lazy fix is the right fix here: no application code inserts into this
-- table. `signup.NewPgxResolver` only ever SELECTs from it, and a repository
-- search finds no other writer in control-plane, edge-api or web-console. The
-- grants are pure attack surface, so they are revoked rather than replaced with
-- a validation layer nothing would call. Registration stays possible for the
-- roles that hold real privilege (the control-plane's own pool, and a database
-- administrator), which is where a decision to trust a domain belongs.
--
-- SELECT is deliberately left in place: a tenant reading back the domains
-- attached to itself is the feature, and the isolation policy already scopes it.
--
-- Domain OWNERSHIP verification, proving the claimant controls the DNS zone or a
-- mailbox in it, is a separate feature and is not attempted here. Removing the
-- ability of an arbitrary user to claim a domain at all is what makes its
-- absence safe in the meantime.

-- Metadata only. No table rewrite, no backfill, no index build: a REVOKE and a
-- DROP POLICY take a brief lock on the table's catalog entry and nothing else,
-- and this table holds a handful of rows on every deployment that exists.
-- Forward-only, per this repository's convention; reversing it is
-- `GRANT INSERT, DELETE ON public.tenant_email_domains TO authenticated;` plus
-- recreating the FOR ALL policy, and should not be done without a domain
-- ownership check in front of it.

BEGIN;

REVOKE INSERT, DELETE ON public.tenant_email_domains FROM authenticated;

-- The FOR ALL policy went with those grants. Read access is unaffected:
-- 20260518_04_phase19_audit_rls_and_indexes.sql already defines
-- tenant_email_domains_select_own with the same tenant predicate, so dropping
-- this one removes a now-duplicate SELECT policy rather than a read path.
-- Leaving a FOR ALL policy behind would also tell the next reader that
-- self-service domain claims are a supported flow, which is exactly the belief
-- this migration exists to remove.
DROP POLICY IF EXISTS tenant_email_domains_isolation ON public.tenant_email_domains;

COMMIT;
