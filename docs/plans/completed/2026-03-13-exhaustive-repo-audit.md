## Goal
Perform a best-effort exhaustive audit of Hive across code, docs, runtime/security posture, product UX, provider strategy, payments/billing correctness, and GitHub execution state, then convert any newly confirmed gaps into documentation updates and high-quality GitHub issues.

## Assumptions
- This pass is still limited to docs, planning, review, and GitHub issue/milestone management unless a blocking runtime defect is discovered during verification.
- Existing audit artifacts and issues `#45` through `#51` are the current baseline, but this pass may refine them or add new issues if the evidence supports it.
- “Exhaustive” here means repo-wide and evidence-driven, not a formal external pentest or production load test.

## Approval Gate
- Status: APPROVED
- Approval source: maintainer replied `APPROVED. $executing-plans` in this session on 2026-03-12.
- Do not execute the Plan section unless that explicit approval is recorded in the session or this file.

## Plan
1. Files: `apps/api/src/**`, `apps/api/test/**`, `apps/api/supabase/**`, `supabase/migrations/**`
   Change: Audit backend correctness and security posture end-to-end, including auth/session validation, user provisioning, settings, API keys, billing, reconciliation, rate limiting, provider routing, observability boundaries, and runtime failure modes.
   Verify: `pnpm --filter @hive/api test`

2. Files: `apps/web/src/**`, `apps/web/e2e/**`
   Change: Audit every major frontend surface and flow, including auth, chat, developer, billing, settings, navigation, responsive behavior, dead-end routes, placeholder copy, and browser-visible data exposure.
   Verify: `NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321 NEXT_PUBLIC_SUPABASE_ANON_KEY="$(npx supabase status -o env | sed -n 's/^ANON_KEY=\"\\(.*\\)\"$/\\1/p')" pnpm --filter @hive/web build`

3. Files: `README.md`, `docs/**`, root policy docs
   Change: Re-read the full current documentation set for drift against implementation and identify stale, conflicting, misleading, duplicated, or underspecified guidance across onboarding, architecture, runbooks, release, and plans.
   Verify: `find docs -type f | sort`

4. Files: runtime configs and local stack commands
   Change: Audit local and deployment-like runtime behavior using Supabase CLI, Docker Compose, and documented verification commands to identify missing prerequisites, misleading setup steps, or silent failure modes.
   Verify: `docker compose -f docker-compose.yml -f docker-compose.dev.yml config`

5. Files: live GitHub issues/milestones/PR state
   Change: Compare the exhaustive audit findings against the current backlog, refine existing issues where needed, create missing issues for newly confirmed gaps, and update the audit artifact with the exact backlog mapping.
   Verify: `gh issue list --state open --limit 100`

6. Files: `docs/audits/2026-03-13-platform-audit.md`, `CHANGELOG.md`, any newly touched docs
   Change: Fold the exhaustive findings back into the audit docs, document newly discovered lessons, and record notable doc/process changes in the changelog.
   Verify: `pnpm --filter @hive/api build` and `pnpm --filter @hive/web build`

## Risks & mitigations
- Risk: the pass becomes a loose collection of observations instead of an execution-grade audit.
  Mitigation: only record findings with code/docs/runtime evidence and map each important gap to an issue or explicit “already tracked” reference.
- Risk: historical docs and generated artifacts add noise.
  Mitigation: treat archived/historical material as context and prioritize current active docs plus live runtime behavior.
- Risk: “exhaustive” drifts into speculative security claims.
  Mitigation: clearly distinguish verified defects, strong inferences, and areas that would require specialist testing beyond repository review.

## Rollback plan
- Revert the exhaustive-audit doc changes if the conclusions or wording are rejected.
- Revert individual GitHub issue edits/creations if maintainers disagree with backlog mapping after review.
