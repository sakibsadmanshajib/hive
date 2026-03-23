## Goal
Temporarily short-circuit web-related failing CI checks while fixing the valid API regressions around v1 auth scope enforcement and streaming chat rate limiting, without weakening the public API’s OpenAI-compatible `/v1/models` contract.

## Assumptions
- The maintainer wants this change to apply repository-wide until a later reversal, not only to PR 88.
- API lint/test/build checks should remain active.
- The preferred short-circuit is to skip web CI execution rather than keep known-failing web jobs running as non-blocking noise.
- OpenAI’s current `/v1/models` endpoint examples still use Bearer auth and its response shape is basic model metadata only, so we should not loosen or expand the public models route for browser-web convenience:
  - https://api.openai.com/v1/models
  - https://api.openai.com/v1/chat/completions
- Of the three review comments, two should be fixed and one should be handled by preserving the public API contract:
  - Do not make public `/v1/models` browser-accessible if that weakens OpenAI-compat auth or response shape.
  - `requireV1ApiPrincipal()` must preserve scope/settings enforcement for scoped v1 routes.
  - streaming chat requests must be rate-limited before provider dispatch.

## Plan
1. Files: `apps/api/src/routes/auth.ts`, `apps/api/src/routes/images-generations.ts`, `apps/api/src/routes/chat-completions.ts`, any other scoped `/v1/*` routes currently calling `requireV1ApiPrincipal()`
Change: Restore scope, permission, and settings enforcement for scoped v1 routes without weakening the current OpenAI-compatible auth behavior of public `/v1/models`.
Verify: `pnpm --filter @hive/api test -- --runInBand test/routes/v1-auth-compliance.test.ts test/routes/models-route.test.ts test/routes/rbac-settings-enforcement.test.ts`

2. Files: `apps/api/src/routes/chat-completions.ts`, related route tests
Change: Move rate limiting ahead of the streaming dispatch path so `stream: true` cannot bypass throttling, while preserving existing SSE behavior and headers.
Verify: `pnpm --filter @hive/api test -- --runInBand test/routes/chat-completions-route.test.ts`

3. Files: `.github/workflows/ci.yml`
Change: Adjust the monorepo CI execution plan so web lint/test/build do not run during the temporary short-circuit period, while API checks and the no-op behavior for unrelated changes remain intact.
Verify: `sed -n '1,260p' .github/workflows/ci.yml`

4. Files: `.github/workflows/web-e2e-smoke.yml`
Change: Disable the dedicated web smoke workflow so pull requests no longer trigger the failing web E2E job during the temporary short-circuit period.
Verify: `sed -n '1,280p' .github/workflows/web-e2e-smoke.yml`

5. Files: `apps/api/src/routes/auth.ts`, `apps/api/src/routes/chat-completions.ts`, `.github/workflows/ci.yml`, `.github/workflows/web-e2e-smoke.yml`, touched tests/docs as needed
Change: Validate that the API regressions are fixed, the web CI short-circuit is isolated to web checks, and the public `/v1/models` contract was not loosened or expanded away from OpenAI compatibility.
Verify: `git diff -- apps/api/src/routes/auth.ts apps/api/src/routes/chat-completions.ts .github/workflows/ci.yml .github/workflows/web-e2e-smoke.yml`

## Risks & mitigations
- Risk: Restoring scope checks in `requireV1ApiPrincipal()` could unintentionally break currently passing v1 routes that relied on the regression.
Mitigation: Re-run targeted auth and route suites for chat, images, and models before broader verification.
- Risk: Moving streaming rate limiting may alter existing stream error timing or headers.
Mitigation: Limit the change to the allow/deny gate before dispatch and verify the chat-completions route tests.
- Risk: Trying to satisfy the browser workspace through the public `/v1/models` surface would drift from the OpenAI contract.
Mitigation: Keep `/v1/models` aligned with the OpenAI spec and short-circuit the temporary web gate instead of changing the public API for web-only needs.
- Risk: Disabling web CI can hide new web regressions.
Mitigation: Limit the short-circuit to web jobs only, preserve API checks, and keep the workflow edits easy to revert when the overhaul starts landing.
- Risk: A partial disable could still leave the PR blocked by another web-triggered workflow.
Mitigation: Update both the monorepo CI web gate and the dedicated web smoke workflow in the same change.
- Risk: The change could unintentionally affect push/manual runs beyond the intended scope.
Mitigation: Keep workflow structure intact and only alter the conditions/triggers tied to web execution.

## Rollback plan
Revert the workflow edits in `.github/workflows/ci.yml` and `.github/workflows/web-e2e-smoke.yml` to restore web test/build and smoke execution once the overhaul reaches a stable verification point, and revert the targeted auth/chat route changes if they introduce contract or authorization regressions.
