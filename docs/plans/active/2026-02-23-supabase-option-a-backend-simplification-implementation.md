# Supabase Option A Backend Simplification Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Migrate auth and core data persistence to Supabase while preserving all existing OpenAI-compatible API behavior and billing/provider runtime semantics.

**Architecture:** Keep Fastify as a thin compatibility gateway and replace selected persistence/auth internals behind existing service interfaces. Introduce feature-flagged Supabase adapters so each domain can be cut over independently with rollback paths. Preserve billing formulas, provider routing, and provider status security boundaries unchanged.

**Tech Stack:** TypeScript, Fastify, pg, Supabase Auth/Postgres (`@supabase/supabase-js`), Redis, Vitest.

---

### Task 1: Add Supabase configuration and client bootstrap

**Files:**
- Modify: `apps/api/src/config/env.ts`
- Modify: `apps/api/.env.example`
- Create: `apps/api/src/runtime/supabase-client.ts`
- Test: `apps/api/test/domain/env.test.ts`

**Step 1: Write the failing test**

```ts
it("reads supabase config and feature flags", () => {
  process.env.SUPABASE_URL = "https://demo.supabase.co";
  process.env.SUPABASE_SERVICE_ROLE_KEY = "service-role-key";
  process.env.SUPABASE_AUTH_ENABLED = "true";
  const env = getEnv();
  expect(env.supabase.url).toBe("https://demo.supabase.co");
  expect(env.supabase.flags.authEnabled).toBe(true);
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @bd-ai-gateway/api test apps/api/test/domain/env.test.ts -t "reads supabase config and feature flags"`
Expected: FAIL because `supabase` fields do not exist in `AppEnv`.

**Step 3: Write minimal implementation**

```ts
supabase: {
  url: required("SUPABASE_URL", "http://127.0.0.1:54321"),
  serviceRoleKey: required("SUPABASE_SERVICE_ROLE_KEY", "dev-service-role"),
  flags: {
    authEnabled: parseBoolean("SUPABASE_AUTH_ENABLED", false),
    userRepoEnabled: parseBoolean("SUPABASE_USER_REPO_ENABLED", false),
    apiKeysEnabled: parseBoolean("SUPABASE_API_KEYS_ENABLED", false),
    billingStoreEnabled: parseBoolean("SUPABASE_BILLING_STORE_ENABLED", false),
  },
}
```

**Step 4: Add Supabase client module**

```ts
export function createSupabaseAdminClient(env: AppEnv) {
  return createClient(env.supabase.url, env.supabase.serviceRoleKey, {
    auth: { autoRefreshToken: false, persistSession: false },
  });
}
```

**Step 5: Run test to verify it passes**

Run: `pnpm --filter @bd-ai-gateway/api test apps/api/test/domain/env.test.ts`
Expected: PASS.

**Step 6: Commit**

```bash
git add apps/api/src/config/env.ts apps/api/.env.example apps/api/src/runtime/supabase-client.ts apps/api/test/domain/env.test.ts
git commit -m "feat(api): add supabase env and admin client bootstrap"
```

### Task 2: Add auth session resolver backed by Supabase

**Files:**
- Create: `apps/api/src/runtime/supabase-auth-service.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Modify: `apps/api/src/routes/auth.ts`
- Test: `apps/api/test/routes/auth-principal.test.ts`

**Step 1: Write the failing test**

```ts
it("accepts bearer token validated through supabase when flag enabled", async () => {
  // stub services.auth.getSessionPrincipal to return null
  // stub services.supabaseAuth.getSessionPrincipal to return { userId: "user_1" }
  // expect requirePrincipal(...) to authenticate successfully
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @bd-ai-gateway/api test apps/api/test/routes/auth-principal.test.ts -t "validated through supabase"`
Expected: FAIL because `supabaseAuth` path is not wired.

**Step 3: Write minimal implementation**

```ts
export class SupabaseAuthService {
  async getSessionPrincipal(token: string): Promise<{ userId: string } | null> {
    // use supabase.auth.getUser(token)
    // map sub -> gateway user id
  }
}
```

**Step 4: Wire service with feature flag fallback**

```ts
const session = env.supabase.flags.authEnabled
  ? await services.supabaseAuth.getSessionPrincipal(bearerToken)
  : await services.auth.getSessionPrincipal(bearerToken);
```

**Step 5: Run tests to verify pass**

Run: `pnpm --filter @bd-ai-gateway/api test apps/api/test/routes/auth-principal.test.ts`
Expected: PASS.

**Step 6: Commit**

```bash
git add apps/api/src/runtime/supabase-auth-service.ts apps/api/src/runtime/services.ts apps/api/src/routes/auth.ts apps/api/test/routes/auth-principal.test.ts
git commit -m "feat(auth): add supabase session principal resolver behind feature flag"
```

### Task 3: Move user profile/settings repository to Supabase

**Files:**
- Create: `apps/api/src/runtime/supabase-user-store.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Modify: `apps/api/src/routes/users.ts`
- Test: `apps/api/test/routes/users-routes.test.ts`
- Test: `apps/api/test/routes/users-settings-routes.test.ts`

**Step 1: Write the failing tests**

```ts
it("returns /v1/users/me from supabase store when user repo flag enabled", async () => {
  // enable SUPABASE_USER_REPO_ENABLED
  // stub supabase user store response
  // assert existing response JSON shape is unchanged
});
```

```ts
it("patch /v1/users/settings writes through supabase repository", async () => {
  // assert settings payload and response contract parity
});
```

**Step 2: Run tests to verify failures**

Run: `pnpm --filter @bd-ai-gateway/api test apps/api/test/routes/users-routes.test.ts apps/api/test/routes/users-settings-routes.test.ts`
Expected: FAIL due to missing Supabase store wiring.

**Step 3: Write minimal implementation**

```ts
export class SupabaseUserStore {
  async findById(userId: string) {}
  async findByEmail(email: string) {}
  async upsertSettings(userId: string, patch: Partial<UserSettings>) {}
  async getSettings(userId: string) {}
}
```

**Step 4: Toggle repository selection in services**

```ts
const users = env.supabase.flags.userRepoEnabled
  ? new SupabaseBackedUserService(supabaseUserStore)
  : new PersistentUserService(store);
```

**Step 5: Run tests to verify pass**

Run: `pnpm --filter @bd-ai-gateway/api test apps/api/test/routes/users-routes.test.ts apps/api/test/routes/users-settings-routes.test.ts`
Expected: PASS with unchanged payload shapes.

**Step 6: Commit**

```bash
git add apps/api/src/runtime/supabase-user-store.ts apps/api/src/runtime/services.ts apps/api/src/routes/users.ts apps/api/test/routes/users-routes.test.ts apps/api/test/routes/users-settings-routes.test.ts
git commit -m "feat(users): add supabase user/settings repository with parity routes"
```

### Task 4: Migrate API key metadata persistence to Supabase (hashed keys)

**Files:**
- Create: `apps/api/src/runtime/supabase-api-key-store.ts`
- Modify: `apps/api/src/runtime/security.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Test: `apps/api/test/domain/api-key-service.test.ts`

**Step 1: Write failing tests**

```ts
it("stores only hashed api keys in supabase", async () => {
  // create key -> persist -> inspect insert payload excludes raw key
});
```

```ts
it("resolves api key by hash and respects revoked flag", async () => {
  // provide known key/hash pair and assert user/scopes resolution
});
```

**Step 2: Run tests to verify failures**

Run: `pnpm --filter @bd-ai-gateway/api test apps/api/test/domain/api-key-service.test.ts`
Expected: FAIL because Supabase API key store not present.

**Step 3: Write minimal implementation**

```ts
export function hashApiKeyForLookup(rawKey: string): string {
  return createHash("sha256").update(rawKey).digest("hex");
}
```

```ts
export class SupabaseApiKeyStore {
  async create(input: { rawKey: string; userId: string; scopes: string[] }) {}
  async resolve(rawKey: string) {}
  async revoke(rawKey: string, userId: string) {}
}
```

**Step 4: Wire feature flag switch**

```ts
if (env.supabase.flags.apiKeysEnabled) {
  // use SupabaseApiKeyStore path
} else {
  // existing PostgresStore path
}
```

**Step 5: Run tests to verify pass**

Run: `pnpm --filter @bd-ai-gateway/api test apps/api/test/domain/api-key-service.test.ts`
Expected: PASS.

**Step 6: Commit**

```bash
git add apps/api/src/runtime/supabase-api-key-store.ts apps/api/src/runtime/security.ts apps/api/src/runtime/services.ts apps/api/test/domain/api-key-service.test.ts
git commit -m "feat(auth): migrate api key metadata persistence to supabase"
```

### Task 5: Add Supabase billing store adapter without changing formulas

**Files:**
- Create: `apps/api/src/runtime/supabase-billing-store.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Test: `apps/api/test/domain/credits-ledger.test.ts`
- Test: `apps/api/test/domain/payment-service.test.ts`

**Step 1: Write failing tests**

```ts
it("applies 1 BDT = 100 credits using supabase store path", async () => {
  // topUp(10) => +1000 credits
});
```

```ts
it("keeps webhook idempotency for duplicate provider_txn_id", async () => {
  // same event_key processed once
});
```

**Step 2: Run tests to verify failures**

Run: `pnpm --filter @bd-ai-gateway/api test apps/api/test/domain/credits-ledger.test.ts apps/api/test/domain/payment-service.test.ts`
Expected: FAIL due to missing Supabase billing adapter.

**Step 3: Write minimal implementation**

```ts
export class SupabaseBillingStore {
  async getBalance(userId: string) {}
  async consumeCredits(userId: string, credits: number, referenceId: string) {}
  async topUp(userId: string, bdtAmount: number, referenceId: string) {}
  async recordPaymentEvent(...) {}
}
```

**Step 4: Keep formula logic in services layer**

```ts
const mintedCredits = Math.trunc(intent.bdtAmount * 100);
```

Do not move this formula into SQL or Supabase triggers.

**Step 5: Run tests to verify pass**

Run: `pnpm --filter @bd-ai-gateway/api test apps/api/test/domain/credits-ledger.test.ts apps/api/test/domain/payment-service.test.ts`
Expected: PASS with unchanged formula behavior.

**Step 6: Commit**

```bash
git add apps/api/src/runtime/supabase-billing-store.ts apps/api/src/runtime/services.ts apps/api/test/domain/credits-ledger.test.ts apps/api/test/domain/payment-service.test.ts
git commit -m "feat(billing): add supabase billing adapter with formula parity"
```

### Task 6: Preserve provider status endpoint security behavior

**Files:**
- Modify: `apps/api/src/routes/providers-status.ts`
- Test: `apps/api/test/routes/providers-status-route.test.ts`

**Step 1: Write failing tests**

```ts
it("keeps public status sanitized during supabase migration", async () => {
  // ensure no detail field leaks
});
```

```ts
it("keeps internal status protected without admin token", async () => {
  // expect 401
});
```

**Step 2: Run tests to verify status protections**

Run: `pnpm --filter @bd-ai-gateway/api test apps/api/test/routes/providers-status-route.test.ts`
Expected: FAIL only if migration touched behavior accidentally.

**Step 3: Apply minimal implementation only if needed**

Keep existing sanitize/filter behavior and token gate exactly as current contract.

**Step 4: Run tests to verify pass**

Run: `pnpm --filter @bd-ai-gateway/api test apps/api/test/routes/providers-status-route.test.ts`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/api/src/routes/providers-status.ts apps/api/test/routes/providers-status-route.test.ts
git commit -m "test(routes): lock provider status security invariants during migration"
```

### Task 7: Add migration SQL for Supabase tables and RLS

**Files:**
- Create: `apps/api/supabase/migrations/20260223_001_auth_user_tables.sql`
- Create: `apps/api/supabase/migrations/20260223_002_api_keys.sql`
- Create: `apps/api/supabase/migrations/20260223_003_billing_tables.sql`
- Modify: `docs/runbooks/auth-rbac-settings-2fa.md`
- Modify: `README.md`

**Step 1: Write failing migration validation check**

```ts
// Optional script or test that asserts required tables/policies exist in local supabase
```

**Step 2: Run migration validation to verify failure**

Run: `pnpm --filter @bd-ai-gateway/api test -t "supabase schema exists"`
Expected: FAIL before migrations are present.

**Step 3: Write minimal migration SQL**

```sql
create table if not exists user_profiles (...);
alter table user_profiles enable row level security;
create policy "users can read own profile" on user_profiles ...;
```

Add equivalent explicit SQL for API keys and billing tables used by adapters.

**Step 4: Run validation to verify pass**

Run: `pnpm --filter @bd-ai-gateway/api test -t "supabase schema exists"`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/api/supabase/migrations docs/runbooks/auth-rbac-settings-2fa.md README.md
git commit -m "docs(api): add supabase migration and operations guidance"
```

### Task 8: End-to-end verification and final build gate

**Files:**
- Modify (if needed): `apps/api/test/routes/*.test.ts`
- Modify (if needed): `apps/api/test/domain/*.test.ts`
- Modify: `docs/release/provider-fallback-matrix.md`

**Step 1: Run targeted migration regression tests**

Run: `pnpm --filter @bd-ai-gateway/api test apps/api/test/routes/auth-principal.test.ts apps/api/test/routes/users-routes.test.ts apps/api/test/routes/users-settings-routes.test.ts apps/api/test/domain/api-key-service.test.ts apps/api/test/domain/credits-ledger.test.ts apps/api/test/domain/payment-service.test.ts apps/api/test/routes/providers-status-route.test.ts`
Expected: PASS.

**Step 2: Run full API suite**

Run: `pnpm --filter @bd-ai-gateway/api test`
Expected: PASS.

**Step 3: Run API build**

Run: `pnpm --filter @bd-ai-gateway/api build`
Expected: PASS.

**Step 4: Optional runtime smoke on Docker stack**

Run: `docker compose up --build -d && docker compose ps`
Expected: API/web/postgres/redis/ollama containers healthy.

**Step 5: Verify provider status security invariants**

Run: `curl -s http://127.0.0.1:8080/v1/providers/status`
Expected: no `detail` in response.

Run: `curl -i -s http://127.0.0.1:8080/v1/providers/status/internal`
Expected: `401` without `x-admin-token`.

**Step 6: Commit**

```bash
git add apps/api/test docs/release/provider-fallback-matrix.md
git commit -m "test(api): verify supabase migration preserves contract and security invariants"
```
