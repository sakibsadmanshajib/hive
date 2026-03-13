# Issue 19 Guest Home Free-Model Access Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make `/` guest-accessible in the web app, add model pricing metadata with `costType`, allow anonymous web chat only for free models, and keep direct API chat access authenticated.

**Architecture:** Extend the API model catalog to carry structured pricing and policy metadata, add a guest-only web chat route that enforces free-model-only access, and update the web chat home to operate in guest mode by default while still expanding to authenticated paid usage when a session exists. Preserve OpenAI-compatible authenticated API behavior and provider status security boundaries.

**Tech Stack:** TypeScript, Fastify API, Next.js app router web app, Vitest, existing runtime/provider services.

---

### Task 1: Define failing API tests for guest chat access policy

**Files:**
- Create: `apps/api/test/routes/guest-chat-route.test.ts`
- Modify: `apps/api/test/routes/chat-completions-route.test.ts`
- Test: `apps/api/test/routes/guest-chat-route.test.ts`
- Test: `apps/api/test/routes/chat-completions-route.test.ts`

**Step 1: Write a failing test for anonymous guest chat using a free model**

Add a route test that posts to the new guest web chat endpoint without auth and expects `200` for a free chat model.

**Step 2: Write a failing test for anonymous guest chat using a paid model**

Add a test that posts to the same route with a fixed-cost or variable-cost model and expects `403`.

**Step 3: Write a failing test confirming `/v1/chat/completions` still requires auth**

Add or update a route test so unauthenticated calls to `/v1/chat/completions` still return `401`.

**Step 4: Run the targeted tests to confirm failure**

Verify: `pnpm --filter @hive/api exec vitest run apps/api/test/routes/guest-chat-route.test.ts apps/api/test/routes/chat-completions-route.test.ts`

### Task 2: Add model cost metadata and guest-access policy helpers

**Files:**
- Modify: `apps/api/src/domain/types.ts`
- Modify: `apps/api/src/domain/model-service.ts`
- Create: `apps/api/test/domain/model-service.test.ts`
- Test: `apps/api/test/domain/model-service.test.ts`

**Step 1: Add richer model metadata types**

Introduce:

- `ModelCostType`
- structured `GatewayModelPricing`
- updated `GatewayModel` shape with `costType` and `pricing`

**Step 2: Update the model catalog**

Replace the current single `creditsPerRequest`-only definitions with:

- at least one `free` chat model
- at least one paid chat model (`fixed` or `variable`)
- existing image model metadata carried forward appropriately

**Step 3: Add guest policy helpers**

Add methods such as:

- `listGuestChatModels()`
- `isGuestAccessible(modelId)`

**Step 4: Run targeted model-service tests**

Verify: `pnpm --filter @hive/api exec vitest run apps/api/test/domain/model-service.test.ts`

### Task 3: Implement guest chat execution in the API without credit consumption

**Files:**
- Modify: `apps/api/src/domain/ai-service.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Modify: `apps/api/src/routes/index.ts`
- Create: `apps/api/src/routes/guest-chat.ts`
- Test: `apps/api/test/routes/guest-chat-route.test.ts`

**Step 1: Add a guest chat path in the AI service**

Add a guest-safe execution method that:

- accepts only chat models
- rejects non-free models
- does not consume credits
- still records safe usage information if appropriate

**Step 2: Register a guest web chat route**

Add a new route intended for browser guest usage only, separate from `/v1/chat/completions`.

**Step 3: Enforce fail-closed free-model policy**

If the requested model is not `costType: "free"`, return `403`.
If no model is provided, default to a free chat model.

**Step 4: Run targeted route tests**

Verify: `pnpm --filter @hive/api exec vitest run apps/api/test/routes/guest-chat-route.test.ts apps/api/test/routes/chat-completions-route.test.ts`

### Task 4: Expose model metadata needed by the web app

**Files:**
- Modify: `apps/api/src/routes/models.ts`
- Modify: `packages/openapi/openapi.yaml`
- Create: `apps/api/test/routes/models-route.test.ts`
- Test: `apps/api/test/routes/models-route.test.ts`

**Step 1: Decide and implement the safe `/v1/models` payload**

Expose enough metadata for the web app to separate free vs paid models, at minimum:

- `id`
- `object`
- `capability`
- `costType`

Optionally expose a sanitized `pricing` object if the UI needs it now.

**Step 2: Add tests for the response shape**

Verify that `/v1/models` returns the new metadata and remains public-safe.

**Step 3: Run targeted route tests**

Verify: `pnpm --filter @hive/api exec vitest run apps/api/test/routes/models-route.test.ts`

### Task 5: Make `/` guest-accessible in the web app

**Files:**
- Inspect/Modify: `apps/web/src/app/page.tsx`
- Inspect/Modify: `apps/web/src/features/chat/*`
- Inspect/Modify: `apps/web/src/features/auth/auth-session.ts`
- Create/Modify: `apps/web/test/chat-auth-gate.test.tsx`
- Create/Modify: `apps/web/test/chat-guest-mode.test.tsx`
- Test: `apps/web/test/chat-auth-gate.test.tsx`
- Test: `apps/web/test/chat-guest-mode.test.tsx`

**Step 1: Remove the redirect behavior that blocks guest access to `/`**

Update the home chat flow so it renders without requiring a session.

**Step 2: Add guest-mode model filtering**

When there is no auth session:

- show only free chat models
- present login/sign-up prompts around paid capabilities

**Step 3: Keep authenticated behavior intact**

When a session exists, continue loading the broader model set and authenticated flow.

**Step 4: Run targeted web tests**

Verify: `pnpm --filter @hive/web exec vitest run apps/web/test/chat-auth-gate.test.tsx apps/web/test/chat-guest-mode.test.tsx`

### Task 6: Wire guest chat requests from the web app

**Files:**
- Inspect/Modify: `apps/web/src/features/chat/use-chat-session.ts`
- Inspect/Modify: `apps/web/src/features/chat/components/message-composer.tsx`
- Inspect/Modify: any existing client API helpers used by chat
- Test: `apps/web/test/chat-guest-mode.test.tsx`

**Step 1: Route guest chat submissions to the new guest backend path**

When no auth session exists, submit chat to the guest route.

**Step 2: Keep authenticated submissions on the current path**

When a session exists, keep using the authenticated chat path.

**Step 3: Add coverage for guest submit behavior**

Verify: `pnpm --filter @hive/web exec vitest run apps/web/test/chat-guest-mode.test.tsx`

### Task 7: Update docs and run required verification

**Files:**
- Modify: `README.md`
- Modify: `CHANGELOG.md`
- Modify: `docs/architecture/system-architecture.md`
- Modify: `docs/design/active/2026-02-24-chat-first-guarded-home.md`

**Step 1: Document guest-first home and free-model policy**

Update repo docs so `/` is no longer described as authenticated-only and model pricing metadata is reflected accurately.

**Step 2: Run touched-scope verification**

Verify:

- `pnpm --filter @hive/api exec vitest run apps/api/test/domain/model-service.test.ts apps/api/test/routes/guest-chat-route.test.ts apps/api/test/routes/chat-completions-route.test.ts apps/api/test/routes/models-route.test.ts`
- `pnpm --filter @hive/api build`
- `pnpm --filter @hive/web exec vitest run apps/web/test/chat-auth-gate.test.tsx apps/web/test/chat-guest-mode.test.tsx`
- `NEXT_PUBLIC_API_BASE_URL=http://127.0.0.1:8080 NEXT_PUBLIC_SUPABASE_URL=http://127.0.0.1:54321 NEXT_PUBLIC_SUPABASE_ANON_KEY=test-supabase-anon-key pnpm --filter @hive/web build`

**Step 3: Run broader required suites if touched behavior expands**

If implementation meaningfully changes auth/bootstrap or smoke flows, also run:

- `pnpm --filter @hive/web test:e2e -- e2e/smoke-auth-chat-billing.spec.ts`
