---
phase: 10
plan: 01
wave: 1
title: Models route auth and differentiator header gap closure
depends_on: []
files_modified:
  - apps/api/src/routes/auth.ts
  - apps/api/src/routes/models.ts
  - apps/api/test/routes/models-route.test.ts
  - apps/api/test/routes/v1-auth-compliance.test.ts
  - apps/api/test/openai-sdk-regression.test.ts
  - apps/api/test/routes/typebox-validation.test.ts
autonomous: true
requirements:
  - FOUND-02
  - DIFF-01

must_haves:
  truths:
    - "GET /v1/models and GET /v1/models/:model require a valid Bearer API key and return 401 authentication_error for missing or invalid keys"
    - "OpenAI SDK client.models.list() with an invalid key throws AuthenticationError instead of returning a list"
    - "All models-route responses, including 200, 401, and 404, include x-model-routed, x-provider-used, x-provider-model, and x-actual-credits headers"
    - "Valid-key models responses keep the existing OpenAI-compliant payload shape and model_not_found 404 behavior"
  artifacts:
    - path: "apps/api/src/routes/auth.ts"
      provides: "requireV1ApiPrincipal with an optional scope parameter so models routes can express 'any valid key'"
      contains: "requiredScope?:"
    - path: "apps/api/src/routes/models.ts"
      provides: "models-route static header helper plus Bearer auth guard on list and retrieve handlers"
      contains: "setModelsRouteHeaders"
    - path: "apps/api/test/routes/models-route.test.ts"
      provides: "route-level coverage for 401/404/200 plus static DIFF-01 headers"
    - path: "apps/api/test/openai-sdk-regression.test.ts"
      provides: "SDK regression coverage proving models.list invalid key throws AuthenticationError"
  key_links:
    - from: "apps/api/src/routes/models.ts"
      to: "apps/api/src/routes/auth.ts"
      via: "requireV1ApiPrincipal(request, reply, services)"
      pattern: "requireV1ApiPrincipal"
    - from: "apps/api/src/routes/models.ts"
      to: "apps/api/test/routes/models-route.test.ts"
      via: "route-level static headers + auth behavior"
      pattern: "x-model-routed|No API key provided|Incorrect API key provided"
    - from: "apps/api/src/routes/models.ts"
      to: "apps/api/test/openai-sdk-regression.test.ts"
      via: "OpenAI SDK invalid-key models.list behavior"
      pattern: "models\\.list\\("
---

## Objective
Close the remaining Phase 10 gaps on `GET /v1/models` and `GET /v1/models/:model` by requiring valid Bearer auth, attaching static differentiator headers to every models-route response, and updating tests that still document the old public-route behavior.

## Context
- `apps/api/src/routes/models.ts` already has compliant list/retrieve serialization and `404 model_not_found` handling.
- `apps/api/src/routes/auth.ts` already has the correct V1 Bearer error messages; models routes simply do not use it yet.
- DIFF-01 for this phase must cover all models-route responses, so the static headers need to be attached before auth failure and before 404s.
- Existing regressions to update:
  - `apps/api/test/openai-sdk-regression.test.ts` still says `models.list()` is public.
  - `apps/api/test/routes/v1-auth-compliance.test.ts` fetches `/v1/models` without auth for its content-type assertion.
  - `apps/api/test/routes/typebox-validation.test.ts` asserts unauthenticated `GET /v1/models` returns `200`.

<task id='T1' wave='1'>
<title>Add optional-scope V1 auth and static models-route headers, with direct route coverage</title>
<files>apps/api/src/routes/auth.ts, apps/api/src/routes/models.ts, apps/api/test/routes/models-route.test.ts</files>
<read_first>
- apps/api/src/routes/auth.ts
- apps/api/src/routes/models.ts
- apps/api/src/routes/chat-completions.ts
- .planning/phases/10-models-route-compliance/10-CONTEXT.md
- .planning/phases/10-models-route-compliance/10-RESEARCH.md
- apps/api/test/routes/models-route.test.ts
</read_first>
<action>
Update `apps/api/src/routes/auth.ts` so `requireV1ApiPrincipal` accepts an optional fourth parameter:

```typescript
requiredScope?: "chat" | "image" | "usage" | "billing"
```

Do not change its missing/invalid-key behavior or error messages.

Update `apps/api/src/routes/models.ts`:
1. Import the auth helper and any reply type needed for a local header helper.
2. Add a local helper named `setModelsRouteHeaders` that always sets:
   - `x-model-routed` to `""`
   - `x-provider-used` to `""`
   - `x-provider-model` to `""`
   - `x-actual-credits` to `"0"`
3. Change both route handlers to accept `(request, reply)`.
4. Call `setModelsRouteHeaders(reply)` at the very top of both handlers.
5. Call `requireV1ApiPrincipal(request, reply, services)` with no scope argument in both handlers; if it returns `undefined`, return immediately.
6. Keep the existing success payload shapes exactly as they are today.
7. Keep the existing retrieve-route 404 behavior:
   - `sendApiError(reply, 404, ... , { type: "invalid_request_error", code: "model_not_found" })`

Update `apps/api/test/routes/models-route.test.ts`:
1. Extend the fake services so models-route handlers can resolve a valid API key.
2. Add request headers with `authorization: "Bearer sk-test"` in successful list/retrieve tests.
3. Add explicit missing-auth and invalid-auth tests for both list and retrieve behavior as needed.
4. Assert the four static headers are present on:
   - successful list response
   - successful retrieve response
   - 404 retrieve response
   - 401 auth failure response
5. Preserve the existing payload-shape assertions for successful responses.
</action>
<done>
- `requireV1ApiPrincipal` can be called without a scope.
- Both models handlers set the four static DIFF-01 headers before auth or lookup.
- `models-route.test.ts` proves 200/401/404 behavior and header presence.
</done>
<verify>cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run apps/api/test/routes/models-route.test.ts</verify>
<acceptance_criteria>
- `apps/api/src/routes/auth.ts` contains `requiredScope?: "chat" | "image" | "usage" | "billing"`
- `apps/api/src/routes/models.ts` contains `function setModelsRouteHeaders`
- `apps/api/src/routes/models.ts` contains `.header("x-model-routed", "")`
- `apps/api/src/routes/models.ts` contains `.header("x-provider-used", "")`
- `apps/api/src/routes/models.ts` contains `.header("x-provider-model", "")`
- `apps/api/src/routes/models.ts` contains `.header("x-actual-credits", "0")`
- Both models handlers call `setModelsRouteHeaders(reply)` before `requireV1ApiPrincipal(`
- `apps/api/src/routes/models.ts` still contains `code: "model_not_found"`
- `pnpm --filter @hive/api exec vitest run apps/api/test/routes/models-route.test.ts` exits 0
</acceptance_criteria>
</task>

<task id='T2' wave='2'>
<title>Update auth, SDK regression, and validation suites to match protected models routes</title>
<files>apps/api/test/routes/v1-auth-compliance.test.ts, apps/api/test/openai-sdk-regression.test.ts, apps/api/test/routes/typebox-validation.test.ts</files>
<read_first>
- apps/api/test/routes/v1-auth-compliance.test.ts
- apps/api/test/openai-sdk-regression.test.ts
- apps/api/test/routes/typebox-validation.test.ts
- apps/api/test/routes/models-route.test.ts
- .planning/phases/10-models-route-compliance/10-CONTEXT.md
- .planning/phases/10-models-route-compliance/10-RESEARCH.md
</read_first>
<action>
Update `apps/api/test/routes/v1-auth-compliance.test.ts`:
1. Keep the valid-bearer `client.models.list()` success test.
2. Add or adjust assertions so missing and invalid Bearer auth on models routes produce `401 authentication_error`.
3. Change the content-type test so it requests `/v1/models` with `Authorization: Bearer ${VALID_KEY}` rather than unauthenticated fetch.

Update `apps/api/test/openai-sdk-regression.test.ts`:
1. Replace the test named `models.list() with any key returns a model list (public endpoint)` with an invalid-key regression:
   - `new OpenAI({ apiKey: "invalid-key", ... }).models.list()` must throw `OpenAI.AuthenticationError`
   - assert `status === 401`
2. Keep or strengthen the valid-key `models.retrieve("mock-chat")` success coverage.
3. Keep the valid-key `models.retrieve("nonexistent-model-id")` 404 coverage.

Update `apps/api/test/routes/typebox-validation.test.ts`:
1. Rename the models validation test to reflect auth-protected behavior.
2. Change its assertion from `statusCode === 200` to `statusCode !== 400`, because this suite is validating schema behavior, not auth behavior.
3. Do not add a fake successful auth path here unless necessary; keeping the test auth-agnostic preserves its purpose.
</action>
<done>
- No test suite still documents `GET /v1/models` as public.
- Auth and SDK suites prove invalid-key models access fails with 401/AuthenticationError.
- TypeBox validation still proves models route does not fail schema validation.
</done>
<verify>cd /home/sakib/hive && pnpm --filter @hive/api exec vitest run apps/api/test/routes/v1-auth-compliance.test.ts apps/api/test/openai-sdk-regression.test.ts apps/api/test/routes/typebox-validation.test.ts</verify>
<acceptance_criteria>
- `apps/api/test/openai-sdk-regression.test.ts` no longer contains `public endpoint`
- `apps/api/test/openai-sdk-regression.test.ts` contains `OpenAI.AuthenticationError` in the invalid-key models.list test
- `apps/api/test/routes/v1-auth-compliance.test.ts` fetches `/v1/models` with `Authorization: Bearer ${VALID_KEY}` for the content-type assertion
- `apps/api/test/routes/typebox-validation.test.ts` contains `expect(res.statusCode).not.toBe(400)` for the models route assertion
- `pnpm --filter @hive/api exec vitest run apps/api/test/routes/v1-auth-compliance.test.ts apps/api/test/openai-sdk-regression.test.ts apps/api/test/routes/typebox-validation.test.ts` exits 0
</acceptance_criteria>
</task>

<task id='T3' wave='3'>
<title>Run full API verification and Docker-only API build</title>
<files>apps/api/src/routes/auth.ts, apps/api/src/routes/models.ts, apps/api/test/routes/models-route.test.ts, apps/api/test/routes/v1-auth-compliance.test.ts, apps/api/test/openai-sdk-regression.test.ts, apps/api/test/routes/typebox-validation.test.ts</files>
<read_first>
- AGENTS.md
- apps/api/src/routes/auth.ts
- apps/api/src/routes/models.ts
- apps/api/test/routes/models-route.test.ts
- apps/api/test/routes/v1-auth-compliance.test.ts
- apps/api/test/openai-sdk-regression.test.ts
- apps/api/test/routes/typebox-validation.test.ts
</read_first>
<action>
Run final verification in this order:
1. `cd /home/sakib/hive && pnpm --filter @hive/api test`
2. `cd /home/sakib/hive && docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"`

If the Docker build command fails because the stack is not up, start or refresh the Docker-local stack using the repo's standard workflow before re-running the build. Do not replace the Docker build with a local host build.

Record the exact failing suite or command if either verification step fails. Do not claim phase readiness until both commands pass.
</action>
<done>
- Full API suite passes after the models-route auth change.
- Docker-container API build passes against the same tree.
</done>
<verify>cd /home/sakib/hive && pnpm --filter @hive/api test && docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"</verify>
<acceptance_criteria>
- `pnpm --filter @hive/api test` exits 0
- `docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"` exits 0
- No modified test still asserts that unauthenticated `GET /v1/models` returns `200`
</acceptance_criteria>
</task>

## Verification

Focused route verification:

```bash
pnpm --filter @hive/api exec vitest run apps/api/test/routes/models-route.test.ts
pnpm --filter @hive/api exec vitest run apps/api/test/routes/v1-auth-compliance.test.ts apps/api/test/openai-sdk-regression.test.ts apps/api/test/routes/typebox-validation.test.ts
```

Final phase verification:

```bash
pnpm --filter @hive/api test
docker compose exec api sh -c "cd /app && pnpm --filter @hive/api build"
```
