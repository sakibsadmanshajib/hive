## Goal
Separate first-time local bootstrap from daily stack startup, organize the crowded `docs/plans` surface, and make sure scripts, tests, and env expectations stay coherent with the standardized local workflow.

## Assumptions
- `pnpm stack:dev` should remain the canonical daily-development entry point.
- First-time setup should use a separate explicit bootstrap command that owns local Supabase schema initialization.
- This task is limited to scripts, package commands, tests, and documentation; no product runtime behavior should change beyond local developer workflow.
- `docs/plans` should distinguish active/current plans from historical or completed artifacts more clearly than it does today.

## Plan
1. Files: [package.json](/home/sakib/hive/package.json), [tools/dev/stack-dev.sh](/home/sakib/hive/tools/dev/stack-dev.sh)
   Change: Audit the current local workflow entry points, env handoff, and verification commands so the new bootstrap/daily split is grounded in how the repo actually starts today.
   Verify: `rg -n "stack:dev|stack:down|stack:reset|NEXT_PUBLIC_|SUPABASE_" package.json tools/dev/stack-dev.sh README.md docs apps`

2. Files: [package.json](/home/sakib/hive/package.json), [tools/dev/bootstrap-local.sh](/home/sakib/hive/tools/dev/bootstrap-local.sh), [tools/dev/stack-dev.sh](/home/sakib/hive/tools/dev/stack-dev.sh)
   Change: Add a dedicated local bootstrap command and script so first-time setup is separate from daily `stack:dev` startup, with explicit Supabase schema/bootstrap behavior.
   Verify: `bash -n tools/dev/stack-dev.sh && bash -n tools/dev/bootstrap-local.sh`

3. Files: [README.md](/home/sakib/hive/README.md), [CONTRIBUTING.md](/home/sakib/hive/CONTRIBUTING.md), [docs/README.md](/home/sakib/hive/docs/README.md), [docs/architecture/system-architecture.md](/home/sakib/hive/docs/architecture/system-architecture.md)
   Change: Rewrite onboarding/dev sections so first-time setup points to the bootstrap command, daily work points to `pnpm stack:dev`, and the Docker-vs-Supabase ownership model is described consistently.
   Verify: `rg -n "First-Time Setup|Daily Development|bootstrap:local|stack:dev|Supabase CLI" README.md CONTRIBUTING.md docs/README.md docs/architecture/system-architecture.md`

4. Files: [docs/runbooks/active/web-e2e-smoke.md](/home/sakib/hive/docs/runbooks/active/web-e2e-smoke.md), [apps/web/e2e/smoke-auth-chat-billing.spec.ts](/home/sakib/hive/apps/web/e2e/smoke-auth-chat-billing.spec.ts), [apps/web/playwright.config.ts](/home/sakib/hive/apps/web/playwright.config.ts)
   Change: Reconcile the smoke runbook with the actual test/runtime expectations, especially around production-style validation, required env vars, and whether `stack:dev` should appear in smoke guidance at all.
   Verify: `rg -n "E2E_|stack:dev|production|next start|docker compose" docs/runbooks/active/web-e2e-smoke.md apps/web/e2e/smoke-auth-chat-billing.spec.ts apps/web/playwright.config.ts`

5. Files: [docs/plans/README.md](/home/sakib/hive/docs/plans/README.md), [docs/plans](/home/sakib/hive/docs/plans), [docs/plans/active](/home/sakib/hive/docs/plans/active)
   Change: Reorganize the crowded `docs/plans` surface into a clearer structure for active/current versus completed/historical plan artifacts, and update the plans index accordingly.
   Verify: `find docs/plans -maxdepth 2 -type f | sort`

6. Files: [CHANGELOG.md](/home/sakib/hive/CHANGELOG.md), [README.md](/home/sakib/hive/README.md), [docs/runbooks/active/web-e2e-smoke.md](/home/sakib/hive/docs/runbooks/active/web-e2e-smoke.md), [package.json](/home/sakib/hive/package.json)
   Change: Record the workflow standardization follow-up and run the final verification pass for scripts, env assumptions, and touched docs.
   Verify: `docker compose -f docker-compose.yml -f docker-compose.dev.yml config && pnpm --filter @hive/api build && NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321 NEXT_PUBLIC_SUPABASE_ANON_KEY=test-supabase-anon-key pnpm --filter @hive/web build`

## Risks & mitigations
- Risk: the new bootstrap command could be destructive if it resets local Supabase state unexpectedly.
  Mitigation: make the command name explicit, document its purpose as first-time/bootstrap setup, and avoid silently calling it from `stack:dev`.
- Risk: docs drift persists if some entry points still describe `stack:dev` as first-time setup.
  Mitigation: update root docs plus the smoke runbook in the same change and verify with targeted grep checks.
- Risk: contributors confuse dev-stack validation with production-style smoke validation.
  Mitigation: explicitly label the smoke runbook as production-bundle validation and keep `stack:dev` framed as daily development only.
- Risk: reorganizing `docs/plans` could break references or make recent plans harder to find.
  Mitigation: keep the structure shallow, update the index in the same change, and preserve dated filenames.
- Risk: env/test assumptions remain inconsistent even after doc fixes.
  Mitigation: inspect Playwright config, smoke fixtures, package scripts, and build commands before finalizing the new workflow text.

## Rollback plan
- Revert the bootstrap script and `package.json` script changes.
- Move plan files back to their prior locations if the reorganization proves noisy.
- Restore the previous docs wording if the new workflow proves too heavy.
- Re-run the verification commands above to confirm the repo is back to the prior documented state.
