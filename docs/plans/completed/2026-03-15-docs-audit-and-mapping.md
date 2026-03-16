# Docs Audit & Commit Mapping — 2026-03-15

Every doc in `docs/` mapped to the implementation commit that realizes or anchors it.
Commit links: `https://github.com/sakibsadmanshajib/hive/commit/<SHA>`

---

## plans/completed/

| Doc | Implementation commit | What it shipped |
|-----|----------------------|-----------------|
| `2026-02-23-chat-first-frontend-information-architecture-design.md` | [`8022a7b`](https://github.com/sakibsadmanshajib/hive/commit/8022a7b) | feat(web): deliver chat-first IA with developer and settings workspaces |
| `2026-02-23-chat-first-frontend-information-architecture-implementation.md` | [`8022a7b`](https://github.com/sakibsadmanshajib/hive/commit/8022a7b) | same — implementation record of that delivery |
| `2026-02-23-supabase-option-a-backend-simplification-design.md` | [`710409f`](https://github.com/sakibsadmanshajib/hive/commit/710409f) | feat(api): add supabase billing adapter and parity safeguards |
| `2026-02-23-supabase-option-a-backend-simplification-implementation.md` | [`5e48370`](https://github.com/sakibsadmanshajib/hive/commit/5e48370) | remove old PostgresStore and integration of local Supabase and langfuse |
| `2026-02-24-chat-first-guarded-home-design.md` | [`db806e2`](https://github.com/sakibsadmanshajib/hive/commit/db806e2) | feat(web): move to guarded chat-first home and modern workspace flow (#15) |
| `2026-02-24-chat-first-guarded-home-implementation.md` | [`db806e2`](https://github.com/sakibsadmanshajib/hive/commit/db806e2) | same |
| `2026-02-24-ci-quality-and-pr-cleanup-design.md` | [`9bfd5ad`](https://github.com/sakibsadmanshajib/hive/commit/9bfd5ad) | chore(ci): optimize smoke workflow cache reuse (#29) |
| `2026-02-24-ci-quality-and-pr-cleanup-implementation.md` | [`9bfd5ad`](https://github.com/sakibsadmanshajib/hive/commit/9bfd5ad) | same |
| `2026-02-24-free-tier-zero-cost-access-design.md` | [`8d26873`](https://github.com/sakibsadmanshajib/hive/commit/8d26873) | feat: add provider-backed guest-free and fix guest auth flow (#61) |
| `2026-02-24-free-tier-zero-cost-access-implementation.md` | [`8d26873`](https://github.com/sakibsadmanshajib/hive/commit/8d26873) | same |
| `2026-02-24-provider-timeout-retry-controls-design.md` | [`00e978c`](https://github.com/sakibsadmanshajib/hive/commit/00e978c) | fix(providers): add timeout and retry controls with safe defaults (#26) |
| `2026-02-24-provider-timeout-retry-controls-implementation.md` | [`00e978c`](https://github.com/sakibsadmanshajib/hive/commit/00e978c) | same |
| `2026-02-24-public-beta-mvp-gap-and-oss-organization-implementation.md` | [`a166dc5`](https://github.com/sakibsadmanshajib/hive/commit/a166dc5) | docs: add planning artifacts, free-tier design docs, and CI cleanup workflows (#20) |
| `2026-02-24-web-e2e-smoke-auth-chat-billing-design.md` | [`4b3c74b`](https://github.com/sakibsadmanshajib/hive/commit/4b3c74b) | feat(web): finish guest-first home flow (#58) — smoke tests finalized here |
| `2026-02-24-web-e2e-smoke-auth-chat-billing-implementation.md` | [`4b3c74b`](https://github.com/sakibsadmanshajib/hive/commit/4b3c74b) | same |
| `2026-02-28-repo-audit-cleanup-plan.md` | [`5a63d04`](https://github.com/sakibsadmanshajib/hive/commit/5a63d04) | chore(audit): verifies claims and implement remainings (#36) |
| `2026-03-09-ci-and-architectural-fixes-design.md` | [`5a63d04`](https://github.com/sakibsadmanshajib/hive/commit/5a63d04) | same audit/cleanup PR |
| `2026-03-09-ci-and-architectural-fixes-implementation.md` | [`5a63d04`](https://github.com/sakibsadmanshajib/hive/commit/5a63d04) | same |
| `2026-03-11-issue-7-payment-reconciliation-scheduler-and-drift-alerts-design.md` | [`a79fcf0`](https://github.com/sakibsadmanshajib/hive/commit/a79fcf0) | feat(api): add payment reconciliation scheduler (#41) |
| `2026-03-11-issue-7-payment-reconciliation-scheduler-and-drift-alerts.md` | [`a79fcf0`](https://github.com/sakibsadmanshajib/hive/commit/a79fcf0) | same |
| `2026-03-11-pr36-remaining-unresolved-comments.md` | [`1959478`](https://github.com/sakibsadmanshajib/hive/commit/1959478) | fix: address CodeRabbit review comments on PR #36 |
| `2026-03-11-provider-metrics-endpoints-design.md` | [`fba0163`](https://github.com/sakibsadmanshajib/hive/commit/fba0163) | feat(api): add startup provider model readiness checks (#43) |
| `2026-03-11-provider-metrics-endpoints.md` | [`fba0163`](https://github.com/sakibsadmanshajib/hive/commit/fba0163) | same |
| `2026-03-12-bootstrap-and-smoke-doc-fixes.md` | [`969df86`](https://github.com/sakibsadmanshajib/hive/commit/969df86) | docs(dev): tighten smoke env and plan guidance |
| `2026-03-12-gh-api-skill-split-design.md` | [`1e034c2`](https://github.com/sakibsadmanshajib/hive/commit/1e034c2) | update gh api skill |
| `2026-03-12-gh-api-skill-split.md` | [`1e034c2`](https://github.com/sakibsadmanshajib/hive/commit/1e034c2) | same |
| `2026-03-12-issue-10-contributor-triage-lifecycle-design.md` | [`176343a`](https://github.com/sakibsadmanshajib/hive/commit/176343a) | docs(runbooks): add maintainer issue lifecycle workflow (#44) |
| `2026-03-12-issue-10-contributor-triage-lifecycle.md` | [`176343a`](https://github.com/sakibsadmanshajib/hive/commit/176343a) | same |
| `2026-03-12-issue-5-oss-governance-docs-design.md` | [`73d0575`](https://github.com/sakibsadmanshajib/hive/commit/73d0575) | docs(oss): add governance and contribution policies (#39) |
| `2026-03-12-issue-5-oss-governance-docs.md` | [`73d0575`](https://github.com/sakibsadmanshajib/hive/commit/73d0575) | same |
| `2026-03-12-issue-6-github-templates-and-metadata-design.md` | [`430ab89`](https://github.com/sakibsadmanshajib/hive/commit/430ab89) | docs(oss): add github issue intake and metadata sync (#40) |
| `2026-03-12-issue-6-github-templates-and-metadata.md` | [`430ab89`](https://github.com/sakibsadmanshajib/hive/commit/430ab89) | same |
| `2026-03-12-issue-8-api-key-lifecycle-design.md` | [`a798297`](https://github.com/sakibsadmanshajib/hive/commit/a798297) | feat(auth): add api key lifecycle management and developer visibility (#42) |
| `2026-03-12-issue-8-api-key-lifecycle.md` | [`a798297`](https://github.com/sakibsadmanshajib/hive/commit/a798297) | same |
| `2026-03-12-issue-9-startup-provider-model-readiness-checks-design.md` | [`fba0163`](https://github.com/sakibsadmanshajib/hive/commit/fba0163) | feat(api): add startup provider model readiness checks (#43) |
| `2026-03-12-issue-9-startup-provider-model-readiness-checks.md` | [`fba0163`](https://github.com/sakibsadmanshajib/hive/commit/fba0163) | same |
| `2026-03-13-docs-standardize-local-development.md` | [`ee5b03e`](https://github.com/sakibsadmanshajib/hive/commit/ee5b03e) | docs(dev): standardize unified local stack workflow |
| `2026-03-13-exhaustive-repo-audit.md` | [`d9c8081`](https://github.com/sakibsadmanshajib/hive/commit/d9c8081) | docs(audit): deepen platform review and standardize local auth bootstrap (#52) |
| `2026-03-13-guest-session-attribution-design.md` | [`65d5541`](https://github.com/sakibsadmanshajib/hive/commit/65d5541) | feat: add guest-first web chat attribution |
| `2026-03-13-guest-session-attribution.md` | [`65d5541`](https://github.com/sakibsadmanshajib/hive/commit/65d5541) | same |
| `2026-03-13-hive-audit-and-backlog-design.md` | [`7d47b6e`](https://github.com/sakibsadmanshajib/hive/commit/7d47b6e) | docs(audit): extend platform audit and backlog |
| `2026-03-13-hive-audit-and-backlog.md` | [`7d47b6e`](https://github.com/sakibsadmanshajib/hive/commit/7d47b6e) | same |
| `2026-03-13-image-provider-design.md` | [`164fab9`](https://github.com/sakibsadmanshajib/hive/commit/164fab9) | feat(api): add real image provider integration (#53) |
| `2026-03-13-image-provider-implementation.md` | [`164fab9`](https://github.com/sakibsadmanshajib/hive/commit/164fab9) | same |
| `2026-03-13-issue-13-usage-analytics-support-design.md` | [`16dff18`](https://github.com/sakibsadmanshajib/hive/commit/16dff18) | feat(app): add usage analytics and support snapshot (#56) |
| `2026-03-13-issue-13-usage-analytics-support.md` | [`16dff18`](https://github.com/sakibsadmanshajib/hive/commit/16dff18) | same |
| `2026-03-13-issue-19-guest-home-conversion-design.md` | [`4f3fc95`](https://github.com/sakibsadmanshajib/hive/commit/4f3fc95) | feat(web): complete guest-first home conversion flow |
| `2026-03-13-issue-19-guest-home-conversion.md` | [`4f3fc95`](https://github.com/sakibsadmanshajib/hive/commit/4f3fc95) | same |
| `2026-03-13-issue-19-guest-home-free-models-design.md` | [`4b3c74b`](https://github.com/sakibsadmanshajib/hive/commit/4b3c74b) | feat(web): finish guest-first home flow (#58) |
| `2026-03-13-issue-19-guest-home-free-models.md` | [`4b3c74b`](https://github.com/sakibsadmanshajib/hive/commit/4b3c74b) | same |
| `2026-03-13-supabase-cli-docker-smoke-fix.md` | [`33433fa`](https://github.com/sakibsadmanshajib/hive/commit/33433fa) | fix(dev): align smoke stack with Supabase CLI |
| `2026-03-13-unified-local-stack-design.md` | [`ee5b03e`](https://github.com/sakibsadmanshajib/hive/commit/ee5b03e) | docs(dev): standardize unified local stack workflow |
| `2026-03-13-unified-local-stack.md` | [`ee5b03e`](https://github.com/sakibsadmanshajib/hive/commit/ee5b03e) | same |
| `2026-03-14-guest-auth-and-chat-regressions.md` | [`8d26873`](https://github.com/sakibsadmanshajib/hive/commit/8d26873) | feat: add provider-backed guest-free and fix guest auth flow (#61) |
| `2026-03-14-provider-agnostic-zero-cost-chat-design.md` | [`8d26873`](https://github.com/sakibsadmanshajib/hive/commit/8d26873) | same |
| `2026-03-14-provider-agnostic-zero-cost-chat.md` | [`8d26873`](https://github.com/sakibsadmanshajib/hive/commit/8d26873) | same |
| `2026-03-15-persist-chat-history-across-sessions.md` | [`c6a022c`](https://github.com/sakibsadmanshajib/hive/commit/c6a022c) | feat(chat): persist history, fix hydration/500/JSON parse |

## plans/active/

| Doc | Status | Notes |
|-----|--------|-------|
| `future-implementation-roadmap.md` | Active | Five-phase roadmap; phases 2-5 are future work |
| `2026-02-24-public-beta-mvp-gap-and-oss-organization-design.md` | Active | Gap analysis partially done; execution items remain |

## plans/ (root — in-flight)

| Doc | Status | Notes |
|-----|--------|-------|
| `2026-03-15-docs-structure-cleanup.md` | In-flight | This cleanup task |
| `2026-03-15-persist-chat-history-across-guest-and-user-plan.md` | In-flight | Chat history persistence work |

## plans/archive/pre-supabase/

| Doc | Status | Notes |
|-----|--------|-------|
| `2026-02-23-bd-ai-credit-gateway-implementation.md` | Archived | Pre-Supabase billing design — obsolete |
| `2026-02-23-chat-ui-overhaul-design.md` | Archived | Pre-Supabase chat UI — superseded by chat-first IA |
| `2026-02-23-chat-ui-overhaul-implementation.md` | Archived | same |
| `2026-02-23-google-auth-rbac-settings-2fa-design.md` | Archived | Pre-Supabase auth — superseded by Supabase auth |
| `2026-02-23-google-auth-rbac-settings-2fa-implementation.md` | Archived | same |
| `2026-02-23-hive-design.md` | Archived | Original Hive design — historical reference only |
| `2026-02-24-auth-first-chat-entry-design.md` | Archived | Pre-Supabase auth flow — superseded |
| `2026-02-24-auth-first-chat-entry-implementation.md` | Archived | same |

## design/active/

| Doc | Commit / Status | Notes |
|-----|----------------|-------|
| `product-and-routing.md` | Active | Living product direction doc with open gaps |
| `2026-03-15-persist-chat-history-across-guest-and-user-design.md` | In-flight | Design for current chat history work |

## design/archive/

| Doc | Anchoring commit | Notes |
|-----|-----------------|-------|
| `2026-02-24-chat-first-guarded-home.md` | [`db806e2`](https://github.com/sakibsadmanshajib/hive/commit/db806e2) | Explicitly superseded; guarded home shipped and then evolved to guest-free |
| `2026-02-24-web-flow-critical-review.md` | [`db806e2`](https://github.com/sakibsadmanshajib/hive/commit/db806e2) | Completed UX audit; remediation tracked in issue #14 (closed) |
| `2026-02-28-repo-audit-cleanup-design.md` | [`5a63d04`](https://github.com/sakibsadmanshajib/hive/commit/5a63d04) | Cleanup executed; Python MVP removed |
| `2026-02-28-repo-audit-cleanup-decision-process.md` | [`5a63d04`](https://github.com/sakibsadmanshajib/hive/commit/5a63d04) | Decision recorded and executed |

## architecture/

| Doc | Commit / Status | Notes |
|-----|----------------|-------|
| `system-architecture.md` | [`c6a022c`](https://github.com/sakibsadmanshajib/hive/commit/c6a022c) | Current ground truth — last updated with chat history work |
| `archive/2026-02-28-python-mvp-migration-map.md` | [`972ce66`](https://github.com/sakibsadmanshajib/hive/commit/972ce66) | Historical — Python app removed in this commit |

## audits/

| Doc | Anchoring commit | Notes |
|-----|-----------------|-------|
| `2026-02-28-final-audit-report.md` | [`5a63d04`](https://github.com/sakibsadmanshajib/hive/commit/5a63d04) | Final audit report from repo cleanup PR #36 |
| `2026-02-28-github-triage.md` | [`5a63d04`](https://github.com/sakibsadmanshajib/hive/commit/5a63d04) | GitHub issue triage snapshot from same PR |
| `2026-02-28-redundancy-inventory.md` | [`5a63d04`](https://github.com/sakibsadmanshajib/hive/commit/5a63d04) | Redundancy inventory from same PR |
| `2026-02-28-runtime-claims-matrix.md` | [`5a63d04`](https://github.com/sakibsadmanshajib/hive/commit/5a63d04) | Runtime claims verification from same PR |
| `2026-03-13-platform-audit.md` | [`d9c8081`](https://github.com/sakibsadmanshajib/hive/commit/d9c8081) | Comprehensive platform audit |

## runbooks/active/

| Doc | Anchoring commit | Notes |
|-----|-----------------|-------|
| `api-key-lifecycle.md` | [`a798297`](https://github.com/sakibsadmanshajib/hive/commit/a798297) | Operational runbook for shipped API key feature |
| `ci-and-pr-cleanup-operations.md` | [`9bfd5ad`](https://github.com/sakibsadmanshajib/hive/commit/9bfd5ad) | CI quality gates and PR cleanup operations |
| `credit-ledger-audit.md` | [`a79fcf0`](https://github.com/sakibsadmanshajib/hive/commit/a79fcf0) | Weekly credit ledger integrity checks |
| `github-triage.md` | [`430ab89`](https://github.com/sakibsadmanshajib/hive/commit/430ab89) | GitHub issue triage process |
| `issue-lifecycle.md` | [`176343a`](https://github.com/sakibsadmanshajib/hive/commit/176343a) | Maintainer issue lifecycle workflow |
| `payments-reconciliation.md` | [`a79fcf0`](https://github.com/sakibsadmanshajib/hive/commit/a79fcf0) | Daily payment reconciliation scans |
| `provider-circuit-breaker.md` | [`4a24a04`](https://github.com/sakibsadmanshajib/hive/commit/4a24a04) | Provider circuit breaker states and monitoring |
| `usage-support-tooling.md` | [`16dff18`](https://github.com/sakibsadmanshajib/hive/commit/16dff18) | User-scoped usage analytics and admin support |
| `web-e2e-smoke.md` | [`4b3c74b`](https://github.com/sakibsadmanshajib/hive/commit/4b3c74b) | Playwright e2e smoke tests |

## runbooks/archive/

| Doc | Status | Notes |
|-----|--------|-------|
| `pre-supabase/auth-rbac-settings-2fa.md` | Archived | Pre-Supabase auth runbook — superseded by Supabase auth |

## release/active/

| Doc | Status | Notes |
|-----|--------|-------|
| `beta-launch-checklist.md` | Active | Multiple unchecked items remain (bKash/SSLCOMMERZ, etc.) |
| `provider-fallback-matrix.md` | Active | Reference matrix for current provider fallback behavior |

## engineering/

| Doc | Status | Notes |
|-----|--------|-------|
| `git-and-ai-practices.md` | Active | Standing reference/policy document |

---

## Structural changes made in this audit

1. **Moved from `plans/active/` → `plans/completed/`** (work is shipped):
   - `2026-02-23-chat-first-frontend-information-architecture-design.md`
   - `2026-02-23-chat-first-frontend-information-architecture-implementation.md`
   - `2026-02-23-supabase-option-a-backend-simplification-design.md`
   - `2026-02-23-supabase-option-a-backend-simplification-implementation.md`
   - `2026-02-24-chat-first-guarded-home-implementation.md`

2. **Moved from `design/active/` → `design/archive/`** (completed or superseded):
   - `2026-02-24-chat-first-guarded-home.md`
   - `2026-02-24-web-flow-critical-review.md`
   - `2026-02-28-repo-audit-cleanup-decision-process.md`
   - `2026-02-28-repo-audit-cleanup-design.md`

3. **Moved from `architecture/` → `architecture/archive/`** (historical):
   - `2026-02-28-python-mvp-migration-map.md`

4. **Updated indexes**: `docs/README.md`, `docs/plans/README.md`, `docs/design/README.md`

5. **Fixed stale internal links** in:
   - `docs/audits/2026-03-13-platform-audit.md`
   - `docs/design/archive/2026-02-28-repo-audit-cleanup-decision-process.md`
   - `docs/plans/completed/2026-03-13-issue-19-guest-home-free-models.md`
