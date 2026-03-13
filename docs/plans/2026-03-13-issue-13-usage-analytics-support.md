## Goal
Implement issue #13's first slice by enriching `/v1/usage` with summary analytics and adding an admin-only user troubleshooting snapshot, while preserving the existing public/internal diagnostics boundary.

## Assumptions
- Work will continue on branch `feat/issue-13-usage-analytics` in the primary working tree per maintainer override.
- Read-time aggregation over recent `usage_events` rows is sufficient for the first slice; no new schema or scheduled rollup is needed.
- The admin-only support route will identify users by `:userId` only for this first version.
- Existing developer panel UI can consume a richer `/v1/usage` payload without requiring a full redesign.

## Plan
1. Files: `apps/api/src/runtime/services.ts`, `apps/api/test/domain/persistent-usage-service.test.ts`
   Change: Add a reusable usage analytics summary method in `PersistentUsageService` that aggregates recent usage rows into totals, daily trend, model split, and endpoint split while preserving the existing raw list behavior.
   Verify: `pnpm --filter @hive/api exec vitest apps/api/test/domain/persistent-usage-service.test.ts`

2. Files: `apps/api/src/routes/usage.ts`, `apps/api/test/routes/usage-route.test.ts` or new equivalent route test file, `packages/openapi/openapi.yaml`
   Change: Extend `/v1/usage` to return recent events plus the new summary shape, and document the enriched response in OpenAPI.
   Verify: `pnpm --filter @hive/api exec vitest apps/api/test/routes/usage-route.test.ts`

3. Files: `apps/api/src/routes/support.ts`, `apps/api/src/routes/index.ts`, `apps/api/src/runtime/services.ts`, `apps/api/test/routes/support-route.test.ts`, `packages/openapi/openapi.yaml`
   Change: Add an admin-only `/v1/support/users/:userId` route that returns user basics, credits, usage summary, recent usage events, API keys, and API key events, with `401` on missing or invalid admin token.
   Verify: `pnpm --filter @hive/api exec vitest apps/api/test/routes/support-route.test.ts`

4. Files: `apps/web/src/app/developer/page.tsx`, `apps/web/src/features/billing/components/usage-cards.tsx`
   Change: Update the developer panel to consume the enriched `/v1/usage` response and display the new analytics summary instead of only a usage-event count.
   Verify: `pnpm --filter @hive/web build`

5. Files: `CHANGELOG.md`, `docs/runbooks/active/usage-support-tooling.md` or nearest existing runbook, `README.md` if needed
   Change: Document the new user analytics output, the admin-only support snapshot, and the operator workflow/security boundary.
   Verify: `rg -n "support/users|/v1/usage|analytics|admin-only" CHANGELOG.md README.md docs/runbooks/active docs/plans`

6. Files: API and web files touched above
   Change: Run full verification for touched scopes and confirm no access-control regression on admin-only surfaces.
   Verify: `pnpm --filter @hive/api test`

7. Files: API files touched above
   Change: Confirm the API still builds cleanly after route and service changes.
   Verify: `pnpm --filter @hive/api build`

8. Files: Web files touched above if changed
   Change: Confirm the production web bundle builds with current developer-panel changes.
   Verify: `pnpm --filter @hive/web build`

## Risks & mitigations
- Risk: Aggregation shape drift between user and admin routes.
  Mitigation: Centralize summary generation in the usage service and reuse the same return shape.
- Risk: Support route exposes too much internal data.
  Mitigation: Limit the route to one user snapshot, require `x-admin-token`, and exclude provider internals.
- Risk: Developer panel assumptions break on the enriched `/v1/usage` payload.
  Mitigation: Preserve the raw `data` list and add summary fields alongside it rather than replacing existing keys.
- Risk: OpenAPI and docs fall behind implementation.
  Mitigation: Treat docs as explicit plan steps before final verification.

## Rollback plan
- Revert the support route registration and handler.
- Revert `/v1/usage` response enrichment back to the raw list shape if downstream compatibility issues appear.
- Leave the underlying usage persistence unchanged so rollback is route- and UI-only.
