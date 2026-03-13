# Image Provider Integration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the mock-backed image generation path with a real provider-backed implementation that preserves an OpenAI-compatible `/v1/images/generations` contract and fits Hive's existing provider, billing, and observability patterns.

**Architecture:** Extend the existing provider registry with an image-generation execution path and optional image capability on provider clients. Keep the public route and OpenAPI schema OpenAI-compatible while provider adapters translate requests and responses internally. Reuse the existing provider status, metrics, fallback, and circuit-breaker patterns instead of creating a separate image registry.

**Tech Stack:** TypeScript, Fastify, existing provider adapters in `apps/api/src/providers`, Fastify route tests, runtime service tests, OpenAPI contract docs.

---

### Task 1: Lock down the current image contract with failing tests

**Files:**
- Modify: `apps/api/test/routes`
- Modify: `apps/api/test`
- Reference: `apps/api/src/routes/images-generations.ts`
- Reference: `packages/openapi/openapi.yaml`

**Step 1: Write a failing route test for OpenAI-compatible image request fields**

Add a targeted test file or extend the existing image route coverage to assert the route accepts an OpenAI-style body shape with:

```ts
{
  model: "image-basic",
  prompt: "a lighthouse in fog",
  n: 1,
  size: "1024x1024",
  response_format: "url",
  user: "user-123"
}
```

Assert the response shape is:

```ts
{
  created: expect.any(Number),
  data: [{ url: expect.any(String) }]
}
```

**Step 2: Run the targeted test to verify it fails**

Run: `pnpm --filter @hive/api exec vitest run <targeted-image-route-test>`

Expected: FAIL because the route/service/provider stack only supports the current placeholder shape.

**Step 3: Add a failing test for provider failure behavior**

Add a test that exercises a provider failure path and asserts:

- HTTP status is provider/gateway failure (`502`)
- body does not expose provider-private diagnostics
- credit charging behavior matches the chosen runtime policy

**Step 4: Run the targeted failure test**

Run: `pnpm --filter @hive/api exec vitest run <targeted-image-route-test> -t "provider failure"`

Expected: FAIL because provider-backed image execution does not exist yet.

**Step 5: Commit**

```bash
git add apps/api/test
git commit -m "test(api): define image generation contract"
```

### Task 2: Extend provider types for image capability

**Files:**
- Modify: `apps/api/src/providers/types.ts`
- Test: `apps/api/test/providers`

**Step 1: Write a failing provider-types-adjacent test**

Add a test covering a provider that implements chat only and another provider that implements image generation, verifying the registry can detect unsupported image capability cleanly.

Example expectation:

```ts
await expect(
  registry.imageGeneration("image-basic", {
    prompt: "test",
    responseFormat: "url",
    size: "1024x1024",
    n: 1,
  }),
).rejects.toThrow(/no provider succeeded|unsupported image capability/);
```

**Step 2: Run the targeted provider test**

Run: `pnpm --filter @hive/api exec vitest run <targeted-provider-registry-test>`

Expected: FAIL because no image-generation types or interface exist.

**Step 3: Add minimal image request/response types**

Update `apps/api/src/providers/types.ts` with explicit types similar to:

```ts
export type ProviderImageRequest = {
  model: string;
  prompt: string;
  n: number;
  size?: string;
  responseFormat: "url" | "b64_json";
  user?: string;
};

export type ProviderImageData = {
  url?: string;
  b64Json?: string;
};

export type ProviderImageResponse = {
  created: number;
  data: ProviderImageData[];
  providerModel?: string;
};
```

Extend `ProviderClient` with an optional image-generation method:

```ts
generateImage?(request: ProviderImageRequest): Promise<ProviderImageResponse>;
```

**Step 4: Re-run the targeted provider test**

Run: `pnpm --filter @hive/api exec vitest run <targeted-provider-registry-test>`

Expected: still FAIL, but now for missing registry support rather than missing types.

**Step 5: Commit**

```bash
git add apps/api/src/providers/types.ts apps/api/test/providers
git commit -m "refactor(api): add provider image capability types"
```

### Task 3: Add image execution to the provider registry

**Files:**
- Modify: `apps/api/src/providers/registry.ts`
- Test: `apps/api/test/providers`

**Step 1: Write a failing registry test for image routing**

Add coverage that mirrors current chat routing expectations:

- primary provider used when enabled
- fallback provider used on failure
- providers without `generateImage` are skipped or fail cleanly
- metrics and circuit state record image attempts

Example:

```ts
const result = await registry.imageGeneration("image-basic", {
  prompt: "city skyline",
  n: 1,
  responseFormat: "url",
});

expect(result.providerUsed).toBe("openai");
expect(result.data[0]?.url).toBe("https://example.com/img.png");
```

**Step 2: Run the registry test**

Run: `pnpm --filter @hive/api exec vitest run <targeted-provider-registry-test>`

Expected: FAIL because `ProviderRegistry` has no image path.

**Step 3: Implement `ProviderRegistry.imageGeneration(...)`**

Mirror the existing `chat(...)` flow:

- resolve primary provider from `modelProviderMap`
- build fallback candidates
- skip disabled providers
- respect circuit state
- call `client.generateImage(...)` when present
- record success/failure metrics
- return provider metadata with the image payload

Use a result shape like:

```ts
export type ProviderImageExecutionResult = {
  created: number;
  data: Array<{ url?: string; b64Json?: string }>;
  providerUsed: ProviderName;
  providerModel: string;
};
```

**Step 4: Re-run the registry tests**

Run: `pnpm --filter @hive/api exec vitest run <targeted-provider-registry-test>`

Expected: PASS for image routing and fallback cases.

**Step 5: Commit**

```bash
git add apps/api/src/providers/registry.ts apps/api/test/providers
git commit -m "feat(api): add registry image routing"
```

### Task 4: Implement the first real image adapter

**Files:**
- Create or Modify: `apps/api/src/providers/<provider>-client.ts`
- Modify: `apps/api/src/providers/types.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Modify: `apps/api/src/config`
- Test: `apps/api/test/providers`

**Step 1: Write a failing adapter test for provider request/response translation**

Create adapter tests that stub the provider HTTP response and assert:

- OpenAI-style request fields are translated correctly
- auth headers are set correctly
- `response_format: "url"` maps to a returned URL payload
- `response_format: "b64_json"` maps to a returned base64 payload when supported

**Step 2: Run the adapter test**

Run: `pnpm --filter @hive/api exec vitest run <targeted-provider-adapter-test>`

Expected: FAIL because the adapter does not exist or lacks image support.

**Step 3: Implement the adapter with retry/timeout behavior**

Follow the existing client patterns from `groq-client.ts` and `ollama-client.ts`:

- use `fetchWithRetry`
- keep config explicit (`baseUrl`, `apiKey`, `timeoutMs`, `maxRetries`)
- implement `status()` using a zero-token metadata endpoint
- implement `checkModelReadiness()` using a model-listing endpoint when possible
- implement `generateImage(...)` translating normalized request fields to provider-native payloads

If the selected provider is OpenAI-compatible, prefer shaping the adapter around that transport while still isolating provider-specific fields internally.

**Step 4: Re-run the adapter tests**

Run: `pnpm --filter @hive/api exec vitest run <targeted-provider-adapter-test>`

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/api/src/providers apps/api/test/providers apps/api/src/config apps/api/src/runtime/services.ts
git commit -m "feat(api): add hosted image provider adapter"
```

### Task 5: Wire the provider into runtime configuration and model mapping

**Files:**
- Modify: `apps/api/src/runtime/services.ts`
- Modify: `apps/api/src/domain/model-service.ts`
- Modify: `apps/api/src/config`
- Test: `apps/api/test`

**Step 1: Write a failing runtime test for `image-basic` routing to a real provider**

Add a runtime-focused test proving:

- `image-basic` resolves to the real provider
- provider-native model mapping is respected
- provider metadata headers are available to the route layer

**Step 2: Run the runtime test**

Run: `pnpm --filter @hive/api exec vitest run <targeted-runtime-image-test>`

Expected: FAIL because `image-basic` still points to `mock`.

**Step 3: Update model and provider config**

Change `image-basic` in `apps/api/src/domain/model-service.ts` from:

```ts
provider: "mock"
```

to the first real provider, and update runtime provider maps and fallback order in `apps/api/src/runtime/services.ts` accordingly.

Add any required env parsing in config for:

- provider base URL
- provider API key
- provider model name
- timeout/retry controls if needed

**Step 4: Re-run the runtime image test**

Run: `pnpm --filter @hive/api exec vitest run <targeted-runtime-image-test>`

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/api/src/runtime/services.ts apps/api/src/domain/model-service.ts apps/api/src/config apps/api/test
git commit -m "feat(api): wire image model to real provider"
```

### Task 6: Replace the placeholder runtime image flow

**Files:**
- Modify: `apps/api/src/runtime/services.ts`
- Test: `apps/api/test`

**Step 1: Write a failing runtime-service test for real provider-backed image generation**

Add coverage for:

- success path returns `created` and `data`
- `x-actual-credits` is set
- provider headers (`x-provider-used`, `x-provider-model`) are preserved if the route already uses them elsewhere
- failed provider path does not look like a fabricated `example.invalid` success

**Step 2: Run the targeted runtime-service test**

Run: `pnpm --filter @hive/api exec vitest run <targeted-runtime-image-test>`

Expected: FAIL because `RuntimeAiService.imageGeneration(...)` still fabricates the payload.

**Step 3: Implement minimal runtime-service changes**

Refactor `imageGeneration(...)` to:

- resolve the model
- calculate credits
- call `providerRegistry.imageGeneration(...)`
- record usage with the routed model
- return an OpenAI-compatible image response body
- include `x-actual-credits`, and if supported by existing route conventions, provider metadata headers

Be explicit about when credits are consumed. If current services cannot reserve/refund, document and test the chosen failure semantics.

**Step 4: Re-run the runtime-service test**

Run: `pnpm --filter @hive/api exec vitest run <targeted-runtime-image-test>`

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/api/src/runtime/services.ts apps/api/test
git commit -m "feat(api): replace mock image runtime flow"
```

### Task 7: Upgrade the image route request and response contract

**Files:**
- Modify: `apps/api/src/routes/images-generations.ts`
- Test: `apps/api/test/routes`
- Modify: `packages/openapi/openapi.yaml`

**Step 1: Write a failing route validation test**

Cover:

- `model`, `prompt`, `n`, `size`, `response_format`, `user`
- defaulting behavior when fields are omitted
- invalid `response_format` rejected
- auth compatibility behavior if bearer token support is added

**Step 2: Run the route validation test**

Run: `pnpm --filter @hive/api exec vitest run <targeted-image-route-test>`

Expected: FAIL because the route currently only types `prompt`.

**Step 3: Update the route body handling and OpenAPI schema**

Modify `apps/api/src/routes/images-generations.ts` to normalize the OpenAI-style fields before passing them to the runtime service.

Update `packages/openapi/openapi.yaml` so `/v1/images/generations` describes:

- request fields
- response `data` item variants
- expected auth header behavior
- `401`, `402`, and `502` semantics where appropriate

**Step 4: Re-run the route validation tests**

Run: `pnpm --filter @hive/api exec vitest run <targeted-image-route-test>`

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/api/src/routes/images-generations.ts packages/openapi/openapi.yaml apps/api/test/routes
git commit -m "feat(api): make image endpoint OpenAI compatible"
```

### Task 8: Verify observability boundaries for image-capable providers

**Files:**
- Modify: `apps/api/test/routes`
- Reference: `apps/api/src/routes/providers-status.ts`
- Reference: `apps/api/src/routes/providers-metrics.ts`

**Step 1: Write or extend failing tests for provider status and metrics boundaries**

Ensure image-provider support does not change these guarantees:

- `/v1/providers/status` omits internal `detail`
- `/v1/providers/status/internal` requires valid admin token
- `/v1/providers/metrics` remains sanitized
- internal metrics routes require valid admin token

**Step 2: Run the targeted status/metrics tests**

Run: `pnpm --filter @hive/api exec vitest run <targeted-provider-boundary-test>`

Expected: FAIL only if image-provider wiring changed boundary behavior.

**Step 3: Apply minimal fixes if needed**

Keep public payloads sanitized while allowing internal diagnostics to include the new image provider's readiness or failure details.

**Step 4: Re-run the boundary tests**

Run: `pnpm --filter @hive/api exec vitest run <targeted-provider-boundary-test>`

Expected: PASS.

**Step 5: Commit**

```bash
git add apps/api/test/routes apps/api/src/routes/providers-status.ts apps/api/src/routes/providers-metrics.ts
git commit -m "test(api): preserve provider observability boundaries"
```

### Task 9: Update user-facing and operator documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/plans/active/future-implementation-roadmap.md`
- Modify: `CHANGELOG.md`
- Modify: any relevant operator docs under `docs/`

**Step 1: Write the docs changes**

Update docs to reflect:

- image generation now has a real provider path
- required env vars for the selected provider
- current billing semantics for image requests
- any fallback or readiness behavior operators need to understand

**Step 2: Verify docs match implementation**

Run: `rg -n \"image-basic|mock|images/generations|provider\" README.md CHANGELOG.md docs packages/openapi/openapi.yaml`

Expected: no stale claims that the primary image path is placeholder-only.

**Step 3: Commit**

```bash
git add README.md CHANGELOG.md docs packages/openapi/openapi.yaml
git commit -m "docs: update image provider support"
```

### Task 10: Run full verification for touched scopes

**Files:**
- Reference only

**Step 1: Run targeted API tests**

Run: `pnpm --filter @hive/api exec vitest run <all-image-and-provider-targets>`

Expected: PASS.

**Step 2: Run the full API test suite**

Run: `pnpm --filter @hive/api test`

Expected: PASS.

**Step 3: Run the API build**

Run: `pnpm --filter @hive/api build`

Expected: PASS.

**Step 4: Run web build only if affected**

Run: `pnpm --filter @hive/web build`

Expected: PASS if any shared contract or web-facing docs/build surfaces were affected enough to require it.

**Step 5: Capture final status**

Run: `git status --short`

Expected: clean or only intentional uncommitted changes.

**Step 6: Commit any final verification-only adjustments**

```bash
git add <verification-fix-files>
git commit -m "test: finalize image provider verification"
```
