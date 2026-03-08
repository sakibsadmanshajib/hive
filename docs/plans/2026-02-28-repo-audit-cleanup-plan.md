# Repo Audit and Cleanup Plan

## Status
**Pending Execution** - This plan and associated design docs were merged in PR #36 to address Issue #35, but implementation was deferred. All steps below are currently pending.

## Goal
Execute a full repository audit and cleanup that verifies implementation claims against code/tests/GitHub, removes redundant/stale assets, updates contracts/docs, and includes an explicit migration/removal track for the legacy Python MVP in this pre-production environment.

## Assumptions
- This is pre-production; disruptive cleanup is acceptable.
- No production data/state retention is required.
- We can remove legacy implementation paths if we leave a clear migration/archive note.
- Canonical implementation target is TypeScript monorepo (`apps/api`, `apps/web`, `packages/*`).
- GitHub CLI access remains available for issue/PR triage verification.

## Plan
1. Files: `docs/audits/2026-02-28-runtime-claims-matrix.md`, `README.md`, `apps/api/src/routes/**`, `apps/web/src/app/**`, `packages/openapi/openapi.yaml`, `openapi/openapi.yaml`
Change: Create a claims matrix (Implemented / Missing / Drift / Bug-risk) by reconciling docs + GitHub claims with actual routes, flows, headers, and tests.
Verify: `rg -n "app\\.(get|post|patch|delete)\\(" apps/api/src/routes && rg -n "GET /|POST /|/v1/" README.md packages/openapi/openapi.yaml openapi/openapi.yaml`

2. Files: `packages/openapi/openapi.yaml`, `openapi/openapi.yaml`, `README.md`, `AGENTS.md`, `docs/**`
Change: Canonicalize OpenAPI to one source of truth, merge required details, remove/retire duplicate contract file, and update all references.
Verify: `rg -n "packages/openapi/openapi.yaml|openapi/openapi.yaml" README.md AGENTS.md docs -g '!**/node_modules/**'`

3. Files: `README.md`, `packages/openapi/openapi.yaml`, `apps/api/src/routes/google-auth.ts`, `apps/api/src/routes/two-factor.ts`
Change: Align auth/2FA endpoint documentation with actual API shape (including `/v1/2fa/*` vs `/v1/auth/2fa/*`) and remove/resolve undocumented nonexistent routes.
Verify: `rg -n "/v1/auth/session|/v1/auth/2fa|/v1/2fa" README.md packages/openapi/openapi.yaml apps/api/src/routes`

4. Files: `packages/openapi/openapi.yaml`, `apps/api/src/routes/chat-completions.ts`, `apps/api/src/runtime/services.ts`, `apps/api/src/domain/ai-service.ts`
Change: Reconcile response-header contract to actual runtime behavior (`x-model-routed`, `x-provider-used`, `x-provider-model`, `x-actual-credits`) and drop stale headers.
Verify: `rg -n "x-model-routed|x-provider-used|x-provider-model|x-actual-credits|x-routing-policy-version|x-estimated-credits" packages/openapi/openapi.yaml apps/api/src`

5. Files: `docs/plans/**`, `docs/plans/active/**`, `docs/plans/README.md`, `docs/README.md`
Change: Remove or archive duplicate plan files where `docs/plans` and `docs/plans/active` diverge for the same basename; keep one canonical version per plan.
Verify: `comm -12 <(ls docs/plans | sort) <(ls docs/plans/active | sort)`

6. Files: GitHub issues/PR metadata, `docs/audits/2026-02-28-github-triage.md`
Change: Triage stale/conflicting GitHub issues and link each status update to merged PR evidence (delivered, superseded, blocked, or still-needed).
Verify: `gh issue list --state all --limit 100 --repo sakibsadmanshajib/hive && gh pr list --state all --limit 100 --repo sakibsadmanshajib/hive`

7. Files: `docs/audits/2026-02-28-redundancy-inventory.md`, `apps/api/src/**`, `apps/web/src/**`, `docs/**`
Change: Produce a redundancy inventory (dead code, duplicate docs/contracts, stale compatibility shims, conflicting comments) and execute low-risk removals.
Verify: `rg -n "TODO|FIXME|deprecated|legacy|compatibility" apps/api/src apps/web/src docs`

8. Files: `docs/architecture/2026-02-28-python-mvp-migration-map.md`, `README.md`, `CHANGELOG.md`
Change: Add an explicit Python MVP migration/removal map showing former Python modules and TypeScript equivalents, with rationale and operator notes.
Verify: `rg -n "Python MVP|legacy Python|app/|tests/" docs README.md CHANGELOG.md`

9. Files: `app/**`, `tests/**` (legacy Python), `.gitignore`, `README.md`, `CHANGELOG.md`, `docs/**`
Change: Remove legacy Python MVP implementation and root Python tests; update docs and ignore rules accordingly.
Verify: `rg --files app tests || true && test ! -d app && test ! -d tests`

10. Files: `apps/api/test/**`, `apps/web/test/**`, optionally `apps/web/e2e/**`
Change: Add/adjust tests where cleanup changes behavior/contracts (especially docs-contract parity and provider status/auth boundaries).
Verify: `pnpm --filter @hive/api test && pnpm --filter @hive/web test`

11. Files: `apps/api/**`, `apps/web/**`, `docker-compose.yml` (if needed for integration checks)
Change: Run full verification sweep after cleanup and doc updates.
Verify: `pnpm --filter @hive/api build && pnpm --filter @hive/web build`

12. Files: `docs/audits/2026-02-28-final-audit-report.md`, `README.md`, `CHANGELOG.md`
Change: Publish final audit report listing verified implemented features, missing features needing creation, confirmed bugs/risks, and resolved cleanup items.
Verify: `rg -n "Implemented|Missing|Bug|Risk|Removed|Archived" docs/audits/2026-02-28-final-audit-report.md && git status --short`

## Risks & mitigations
- Risk: Removing legacy Python paths could delete reference material still needed by contributors.
  - Mitigation: ship migration map + archive note before deletion and keep full git history.
- Risk: Contract/docs cleanup may accidentally change consumer expectations.
  - Mitigation: treat route/header parity as test-backed and verify OpenAPI/README against runtime code before merge.
- Risk: Large cleanup batch may obscure regressions.
  - Mitigation: execute in staged commits by concern (contract, docs, legacy removal, tests, final report).
- Risk: GitHub triage could close issues prematurely.
  - Mitigation: require explicit evidence links (PR/commit/test path) in each triage decision.

## Rollback plan
1. Revert the most recent cleanup commit(s) by concern (`git revert <sha>`), starting from highest-risk deletions.
2. If Python removal must be temporarily undone, restore only `app/` and `tests/` from previous commit while keeping docs/contracts fixes.
3. Re-run baseline verification (`pnpm --filter @hive/api test && pnpm --filter @hive/api build && pnpm --filter @hive/web build`).
4. Re-open or relabel any GitHub issues that were triaged based on reverted changes.
