# Free Tier Zero-Cost Access Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Enforce a zero-cost free/guest experience with no API-key inference access for free/unauthenticated users while preserving paid-user reliability and adding paid low-effort cost optimization.

**Architecture:** Add a server-side policy layer that resolves request principal/channel, validates model eligibility, and routes through labeled provider pools (`free_pool` vs `paid_pool`) with fail-closed controls. Keep OpenAI-compatible response contract unchanged and preserve public/internal provider-status security boundaries. Roll out in phases: policy shadow mode, hard free-boundary enforcement, then paid low-effort free-first optimization.

**Tech Stack:** Fastify + TypeScript (`apps/api`), Vitest, PostgreSQL-backed user/settings services, provider registry routing.

---

### Task 1: Add failing tests for free API-key denial and free-only model gates

**Files:**
- Create: `apps/api/test/routes/free-tier-access-policy.test.ts`
- Modify: `apps/api/test/routes/auth-principal.test.ts`
- Test: `apps/api/test/routes/free-tier-access-policy.test.ts`, `apps/api/test/routes/auth-principal.test.ts`

**Step 1: Add failing test for free account API-key inference denial**

```ts
it("returns 403 when free-tier principal uses x-api-key on chat route", async () => {
  const app = await buildTestApp({
    principal: { userId: "user_free", authType: "apiKey", scopes: ["chat"], tier: "free" },
  });

  const response = await app.inject({
    method: "POST",
    url: "/v1/chat/completions",
    headers: { "x-api-key": "test_key" },
    payload: { model: "paid-fast", messages: [{ role: "user", content: "hi" }] },
  });

  expect(response.statusCode).toBe(403);
  expect(response.json()).toEqual({ error: "forbidden" });
});
```

**Step 2: Add failing test for free/guest model eligibility gate**

```ts
it("returns 403 when free session requests non-free virtual model", async () => {
  const app = await buildTestApp({
    principal: { userId: "user_free", authType: "session", scopes: ["chat"], tier: "free" },
  });

  const response = await app.inject({
    method: "POST",
    url: "/v1/chat/completions",
    headers: { authorization: "Bearer token" },
    payload: { model: "paid-smart", messages: [{ role: "user", content: "hello" }] },
  });

  expect(response.statusCode).toBe(403);
});
```

**Step 3: Run tests to confirm failure**

Run: `pnpm --filter @bd-ai-gateway/api test -- apps/api/test/routes/free-tier-access-policy.test.ts apps/api/test/routes/auth-principal.test.ts`
Expected: FAIL with missing `tier`/policy behavior.

**Step 4: Commit test scaffolding**

```bash
git add apps/api/test/routes/free-tier-access-policy.test.ts apps/api/test/routes/auth-principal.test.ts
git commit -m "test(api): define free-tier api access policy expectations"
```

### Task 2: Introduce request access tier resolution and auth policy checks

**Files:**
- Create: `apps/api/src/runtime/access-tier.ts`
- Modify: `apps/api/src/routes/auth.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Test: `apps/api/test/routes/auth-principal.test.ts`

**Step 1: Add tier types and resolver in `access-tier.ts`**

```ts
export type AccessTier = "guest" | "free" | "paid";

export type RequestChannel = "session" | "apiKey";

export type TierContext = {
  tier: AccessTier;
  channel: RequestChannel;
};

export function isApiInferenceAllowed(ctx: TierContext): boolean {
  return !(ctx.channel === "apiKey" && (ctx.tier === "guest" || ctx.tier === "free"));
}
```

**Step 2: Extend principal shape with `tier` in `auth.ts`**

```ts
export type AuthPrincipal = {
  userId: string;
  authType: "apiKey" | "session";
  scopes: string[];
  tier: "guest" | "free" | "paid";
};
```

**Step 3: Resolve tier from user settings/service and enforce API-key denial**

- Resolve user tier from user settings or profile (default `free` for signed-in accounts unless paid entitlement exists).
- For API-key routes, reject principals where `tier !== "paid"`.

**Step 4: Run targeted auth tests**

Run: `pnpm --filter @bd-ai-gateway/api test -- apps/api/test/routes/auth-principal.test.ts`
Expected: PASS with free-api-key denial behavior.

**Step 5: Commit auth layer changes**

```bash
git add apps/api/src/runtime/access-tier.ts apps/api/src/routes/auth.ts apps/api/src/runtime/services.ts apps/api/test/routes/auth-principal.test.ts
git commit -m "feat(api): enforce paid-only api-key inference access"
```

### Task 3: Add provider/model label catalog and virtual model policy gates

**Files:**
- Create: `apps/api/src/providers/provider-catalog.ts`
- Modify: `apps/api/src/providers/registry.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Test: `apps/api/test/providers/provider-registry.test.ts`
- Test: `apps/api/test/domain/routing-engine.test.ts`

**Step 1: Add provider catalog and virtual model definitions**

```ts
export type CostClass = "zero" | "low" | "paid";
export type PoolMembership = "free_pool" | "paid_pool" | "both";

export type ProviderCatalogEntry = {
  provider: "ollama" | "groq" | "mock";
  providerModel: string;
  costClass: CostClass;
  poolMembership: PoolMembership;
  capabilities: string[];
  stabilityTier: "experimental" | "beta" | "stable";
};

export const VIRTUAL_MODELS = {
  "free-fast": { pool: "free_pool", intent: "latency" },
  "free-balanced": { pool: "free_pool", intent: "quality" },
  "paid-fast": { pool: "paid_pool", intent: "latency" },
  "paid-smart": { pool: "paid_pool", intent: "reasoning" },
} as const;
```

**Step 2: Add hard gate in registry selection**

- Free/guest contexts must only use candidates from `free_pool` and `costClass === "zero"`.
- If no free candidates are healthy, throw controlled no-capacity error.

**Step 3: Add tests for no-cost invariant**

```ts
it("never selects paid provider for free context", async () => {
  const result = await registry.chatWithPolicy({
    modelId: "free-fast",
    tier: "free",
    channel: "session",
    messages: [{ role: "user", content: "hello" }],
  });

  expect(result.providerUsed).not.toBe("groq");
});
```

**Step 4: Run provider tests**

Run: `pnpm --filter @bd-ai-gateway/api test -- apps/api/test/providers/provider-registry.test.ts apps/api/test/domain/routing-engine.test.ts`
Expected: PASS.

**Step 5: Commit provider policy layer**

```bash
git add apps/api/src/providers/provider-catalog.ts apps/api/src/providers/registry.ts apps/api/src/runtime/services.ts apps/api/test/providers/provider-registry.test.ts apps/api/test/domain/routing-engine.test.ts
git commit -m "feat(providers): add virtual-model pool policy and zero-cost guardrails"
```

### Task 4: Enforce chat-route model eligibility and preserve OpenAI-compatible headers

**Files:**
- Modify: `apps/api/src/routes/chat-completions.ts`
- Test: `apps/api/test/routes/free-tier-access-policy.test.ts`
- Test: `apps/api/test/providers/provider-fallback.test.ts`

**Step 1: Add request-level model policy enforcement before dispatch**

```ts
const modelId = request.body?.model ?? "free-fast";
const decision = services.policy.resolveModelAccess({ principal, modelId, endpoint: "chat" });
if (!decision.allowed) {
  return reply.code(403).send({ error: "forbidden" });
}
```

**Step 2: Keep existing response header contract unchanged**

- Continue returning:
  - `x-model-routed`
  - `x-provider-used`
  - `x-provider-model`
  - `x-actual-credits`

**Step 3: Add tests for forbidden non-free model and allowed free model behavior**

Run: `pnpm --filter @bd-ai-gateway/api test -- apps/api/test/routes/free-tier-access-policy.test.ts apps/api/test/providers/provider-fallback.test.ts`
Expected: PASS.

**Step 4: Commit route enforcement**

```bash
git add apps/api/src/routes/chat-completions.ts apps/api/test/routes/free-tier-access-policy.test.ts apps/api/test/providers/provider-fallback.test.ts
git commit -m "feat(routes): enforce free-tier model eligibility on chat completions"
```

### Task 5: Add paid low-effort classifier hook with safe fallback controls

**Files:**
- Create: `apps/api/src/runtime/effort-classifier.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Modify: `apps/api/src/providers/registry.ts`
- Test: `apps/api/test/domain/routing-engine.test.ts`
- Test: `apps/api/test/providers/provider-fallback.test.ts`

**Step 1: Add simple deterministic classifier**

```ts
export type EffortClass = "low_effort" | "standard" | "complex";

export function classifyEffort(messages: Array<{ role: string; content: string }>): EffortClass {
  const text = messages.map((m) => m.content).join(" ").trim();
  if (text.length <= 180) return "low_effort";
  if (/analyze|reason|compare|trade-?off/i.test(text)) return "complex";
  return "standard";
}
```

**Step 2: Apply classifier only for paid contexts**

- For paid + `low_effort`, allow free-first attempt with strict timeout budget.
- On free candidate failure, fallback to paid candidates.

**Step 3: Add tests for free-first and paid fallback behavior**

Run: `pnpm --filter @bd-ai-gateway/api test -- apps/api/test/providers/provider-fallback.test.ts apps/api/test/domain/routing-engine.test.ts`
Expected: PASS.

**Step 4: Commit classifier integration**

```bash
git add apps/api/src/runtime/effort-classifier.ts apps/api/src/runtime/services.ts apps/api/src/providers/registry.ts apps/api/test/providers/provider-fallback.test.ts apps/api/test/domain/routing-engine.test.ts
git commit -m "feat(routing): add paid low-effort free-first optimization with safe fallback"
```

### Task 6: Add abuse controls and policy decision telemetry

**Files:**
- Create: `apps/api/src/runtime/abuse-guard.ts`
- Modify: `apps/api/src/routes/chat-completions.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Test: `apps/api/test/domain/rate-limiter.test.ts`
- Test: `apps/api/test/routes/free-tier-access-policy.test.ts`

**Step 1: Add policy-friendly abuse guard surface**

```ts
export type AbuseDecision = { allowed: boolean; reason?: string };

export async function checkAbuse(input: {
  userId: string;
  ip?: string;
  channel: "session" | "apiKey";
  tier: "guest" | "free" | "paid";
}): Promise<AbuseDecision> {
  // tier-aware limiter thresholds
  return { allowed: true };
}
```

**Step 2: Emit structured routing/policy logs**

- Log fields: `user_tier`, `entry_channel`, `virtual_model`, `provider_selected`, `fallback_reason`, `abuse_decision`.

**Step 3: Add tests for throttle/block behavior under free burst traffic**

Run: `pnpm --filter @bd-ai-gateway/api test -- apps/api/test/domain/rate-limiter.test.ts apps/api/test/routes/free-tier-access-policy.test.ts`
Expected: PASS.

**Step 4: Commit abuse control baseline**

```bash
git add apps/api/src/runtime/abuse-guard.ts apps/api/src/routes/chat-completions.ts apps/api/src/runtime/services.ts apps/api/test/domain/rate-limiter.test.ts apps/api/test/routes/free-tier-access-policy.test.ts
git commit -m "feat(api): add tier-aware abuse guard and routing decision telemetry"
```

### Task 7: Update docs and run full verification gate

**Files:**
- Modify: `README.md`
- Modify: `docs/architecture/system-architecture.md`
- Modify: `docs/plans/README.md`
- Test: `apps/api/test/routes/providers-status-route.test.ts`

**Step 1: Update public docs for free-tier behavior**

- Document that free/guest users are web-session only.
- Document that API-key inference is paid-tier only.
- Document free virtual model family (`free-fast`, `free-balanced`) and no-cost invariant.

**Step 2: Ensure provider status boundary docs remain explicit**

- Public status endpoint remains sanitized.
- Internal status remains token-protected.

**Step 3: Run required verification commands**

Run: `pnpm --filter @bd-ai-gateway/api test -- apps/api/test/routes/providers-status-route.test.ts`
Expected: PASS.

Run: `pnpm --filter @bd-ai-gateway/api test`
Expected: PASS.

Run: `pnpm --filter @bd-ai-gateway/api build`
Expected: PASS.

**Step 4: Commit documentation and final gate proof**

```bash
git add README.md docs/architecture/system-architecture.md docs/plans/README.md
git commit -m "docs(api): document zero-cost free-tier policy and routing boundaries"
```
