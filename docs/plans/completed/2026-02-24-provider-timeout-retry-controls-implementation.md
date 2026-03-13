# Provider Timeout and Retry Controls Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add provider timeout and retry controls with safe defaults for Ollama and Groq provider calls.

**Architecture:** Introduce env-driven timeout/retry settings, implement a shared provider HTTP helper with retry policy, and wire both provider clients through it. Keep routing/fallback contracts unchanged and preserve provider status endpoint security boundaries.

**Tech Stack:** TypeScript, Fastify runtime wiring, Vitest.

---

### Task 1: Add failing env tests for provider timeout/retry settings

**Files:**
- Modify: `apps/api/test/domain/env.test.ts`
- Test: `apps/api/test/domain/env.test.ts`

**Step 1: Write the failing tests**

Add tests that assert:
- default values are `4000ms` timeout and `1` retry for both providers
- provider-specific env overrides are applied correctly

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @hive/api test apps/api/test/domain/env.test.ts`
Expected: FAIL because new env fields are not implemented.

**Step 3: Write minimal env implementation**

Add new env parsing and `AppEnv` fields in `apps/api/src/config/env.ts` for:
- `providers.ollama.timeoutMs`
- `providers.ollama.maxRetries`
- `providers.groq.timeoutMs`
- `providers.groq.maxRetries`

Use shared fallback vars (`PROVIDER_TIMEOUT_MS`, `PROVIDER_MAX_RETRIES`) and provider-specific overrides.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @hive/api test apps/api/test/domain/env.test.ts`
Expected: PASS.

### Task 2: Add failing provider transport tests

**Files:**
- Create: `apps/api/test/providers/provider-http-client.test.ts`
- Test: `apps/api/test/providers/provider-http-client.test.ts`

**Step 1: Write the failing tests**

Add tests for shared provider request behavior:
- retries on retryable HTTP status (e.g., `503`) and succeeds on later attempt
- does not retry on non-retryable status (`400`)
- throws after retry budget is exhausted

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @hive/api test apps/api/test/providers/provider-http-client.test.ts`
Expected: FAIL because helper module does not exist yet.

**Step 3: Write minimal implementation**

Create `apps/api/src/providers/http-client.ts` with:
- timeout support using `AbortController`
- retry loop with `maxRetries`
- retry condition: timeout/network or HTTP `429/5xx`

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @hive/api test apps/api/test/providers/provider-http-client.test.ts`
Expected: PASS.

### Task 3: Integrate helper into provider clients with failing regression test

**Files:**
- Modify: `apps/api/src/providers/ollama-client.ts`
- Modify: `apps/api/src/providers/groq-client.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Modify: `apps/api/test/providers/provider-registry.test.ts` (if constructor typing changes require fixture updates)
- Test: `apps/api/test/providers/provider-fallback.test.ts`

**Step 1: Add or adjust failing test if required**

Add/adjust a provider-level test ensuring retry-enabled clients still surface final errors and allow registry fallback to next provider.

**Step 2: Run targeted tests to verify RED state (if changed)**

Run: `pnpm --filter @hive/api test apps/api/test/providers/provider-fallback.test.ts`
Expected: FAIL if behavior/typing changed and implementation not yet complete.

**Step 3: Implement minimal client wiring**

- Extend client config types to accept timeout/retry values
- Use shared helper for `chat()` and `status()`
- Pass env values in `createRuntimeServices()`

**Step 4: Run targeted provider tests**

Run: `pnpm --filter @hive/api test apps/api/test/providers/provider-http-client.test.ts apps/api/test/providers/provider-fallback.test.ts apps/api/test/providers/provider-registry.test.ts apps/api/test/providers/provider-status.test.ts`
Expected: PASS.

### Task 4: Update operator/contributor docs

**Files:**
- Modify: `README.md`
- Modify: `docs/release/active/provider-fallback-matrix.md` (if trigger text needs to reflect configurable timeout)

**Step 1: Document env controls**

Add the new env variables and default values in README provider configuration section.

**Step 2: Run quick doc sanity check**

Run: `rg -n "PROVIDER_TIMEOUT_MS|PROVIDER_MAX_RETRIES|OLLAMA_TIMEOUT_MS|GROQ_TIMEOUT_MS" README.md`
Expected: new entries found.

### Task 5: Full verification

**Step 1: Run full API test suite**

Run: `pnpm --filter @hive/api test`
Expected: PASS.

**Step 2: Run API build**

Run: `pnpm --filter @hive/api build`
Expected: PASS.

**Step 3: Run web build if web files changed**

Run (only if web affected): `pnpm --filter @hive/web build`
Expected: PASS.

