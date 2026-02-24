# Google Auth + RBAC + User Settings + 2FA Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add backend-managed Google login, permission-matrix authorization, mandatory per-user feature gates, and phase-1 2FA scaffolding without breaking existing API behavior.

**Architecture:** Extend the existing Fastify + runtime service + Postgres store stack by introducing an auth principal model (session or API key), a permission matrix evaluator, and a user settings gate layer that is enforced for every protected feature. Keep current API-key and billing behavior compatible via scope-to-permission bridging while incrementally introducing new auth routes and 2FA endpoints.

**Tech Stack:** Fastify, TypeScript, PostgreSQL (`pg`), Vitest, existing runtime services and route modules.

---

### Task 1: Add RBAC, settings, session, and 2FA schema bootstrap

**Files:**
- Modify: `apps/api/src/runtime/postgres-store.ts`
- Test: `apps/api/test/domain/postgres-schema-auth.test.ts` (new)

**Step 1: Write the failing test**

Create `apps/api/test/domain/postgres-schema-auth.test.ts` with assertions that schema helper methods for auth tables execute and can round-trip minimal rows.

```ts
it("creates and reads user settings rows", async () => {
  await store.upsertUserSetting("user_1", "apiEnabled", true);
  const map = await store.getUserSettings("user_1");
  expect(map.apiEnabled).toBe(true);
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @hive/api test apps/api/test/domain/postgres-schema-auth.test.ts`
Expected: FAIL because table/helper methods do not exist.

**Step 3: Write minimal implementation**
- Add table creation in schema bootstrap for:
  - `permissions`, `roles`, `role_permissions`, `user_roles`
  - `user_settings`
  - `auth_sessions`
  - `user_2fa` and `auth_2fa_challenges`
- Add minimal typed CRUD helpers for each.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @hive/api test apps/api/test/domain/postgres-schema-auth.test.ts`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/api/src/runtime/postgres-store.ts apps/api/test/domain/postgres-schema-auth.test.ts
git commit -m "feat(api): add auth, rbac, settings, and 2fa schema primitives"
```

### Task 2: Build permission matrix service with scope-bridge compatibility

**Files:**
- Create: `apps/api/src/runtime/authorization.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Test: `apps/api/test/domain/authorization-matrix.test.ts` (new)

**Step 1: Write the failing test**

Create `apps/api/test/domain/authorization-matrix.test.ts`:

```ts
it("maps legacy api key scopes to equivalent permissions", async () => {
  const allowed = authorization.hasPermission({ scopes: ["chat"] }, "chat:write");
  expect(allowed).toBe(true);
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @hive/api test apps/api/test/domain/authorization-matrix.test.ts`
Expected: FAIL because authorization service does not exist.

**Step 3: Write minimal implementation**
- Add authorization service with:
  - permission checks from role assignments.
  - legacy scope-to-permission bridge map.
  - helper for route checks.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @hive/api test apps/api/test/domain/authorization-matrix.test.ts`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/api/src/runtime/authorization.ts apps/api/src/runtime/services.ts apps/api/test/domain/authorization-matrix.test.ts
git commit -m "feat(api): add permission matrix evaluator with scope bridge"
```

### Task 3: Add mandatory user settings gate service

**Files:**
- Create: `apps/api/src/runtime/user-settings.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Test: `apps/api/test/domain/user-settings-gates.test.ts` (new)

**Step 1: Write the failing test**

Create `apps/api/test/domain/user-settings-gates.test.ts`:

```ts
it("denies feature when setting is disabled", () => {
  const canUse = settings.canUse("generateImage", { generateImage: false });
  expect(canUse).toBe(false);
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @hive/api test apps/api/test/domain/user-settings-gates.test.ts`
Expected: FAIL because gate service is missing.

**Step 3: Write minimal implementation**
- Add gate evaluator and defaults map.
- Add helpers to fetch/update user settings.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @hive/api test apps/api/test/domain/user-settings-gates.test.ts`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/api/src/runtime/user-settings.ts apps/api/src/runtime/services.ts apps/api/test/domain/user-settings-gates.test.ts
git commit -m "feat(api): add mandatory user settings gate service"
```

### Task 4: Introduce unified auth principal resolution middleware

**Files:**
- Modify: `apps/api/src/routes/auth.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Test: `apps/api/test/routes/auth-principal.test.ts` (new)

**Step 1: Write the failing test**

Create `apps/api/test/routes/auth-principal.test.ts` to assert:
- session bearer token resolves principal.
- `x-api-key` resolves principal.
- `401` for missing credentials.

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @hive/api test apps/api/test/routes/auth-principal.test.ts`
Expected: FAIL because middleware only supports API key scope checks.

**Step 3: Write minimal implementation**
- Replace `requireApiUser` internals with principal resolver + permission + setting hooks.
- Keep backward compatibility function signature adapters for existing routes.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @hive/api test apps/api/test/routes/auth-principal.test.ts`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/api/src/routes/auth.ts apps/api/src/runtime/services.ts apps/api/test/routes/auth-principal.test.ts
git commit -m "refactor(api): unify session and api-key principal resolution"
```

### Task 5: Add Google OAuth routes and service

**Files:**
- Create: `apps/api/src/routes/google-auth.ts`
- Modify: `apps/api/src/routes/index.ts`
- Modify: `apps/api/src/config/env.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Test: `apps/api/test/routes/google-auth-routes.test.ts` (new)

**Step 1: Write the failing test**

Create `apps/api/test/routes/google-auth-routes.test.ts` for:
- `/v1/auth/google/start` returns URL with state.
- callback rejects invalid state.
- callback succeeds with mocked verifier.

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @hive/api test apps/api/test/routes/google-auth-routes.test.ts`
Expected: FAIL because routes/config do not exist.

**Step 3: Write minimal implementation**
- Add env vars:
  - `GOOGLE_CLIENT_ID`
  - `GOOGLE_CLIENT_SECRET`
  - `GOOGLE_REDIRECT_URI`
  - `AUTH_SESSION_TTL_MINUTES`
- Implement start/callback/logout routes and session creation.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @hive/api test apps/api/test/routes/google-auth-routes.test.ts`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/api/src/routes/google-auth.ts apps/api/src/routes/index.ts apps/api/src/config/env.ts apps/api/src/runtime/services.ts apps/api/test/routes/google-auth-routes.test.ts
git commit -m "feat(api): add backend-managed google oauth session routes"
```

### Task 6: Enforce permission + setting gates on core endpoints

**Files:**
- Modify: `apps/api/src/routes/chat-completions.ts`
- Modify: `apps/api/src/routes/images-generations.ts`
- Modify: `apps/api/src/routes/usage.ts`
- Modify: `apps/api/src/routes/credits-balance.ts`
- Modify: `apps/api/src/routes/payment-intents.ts`
- Modify: `apps/api/src/routes/payment-demo-confirm.ts`
- Modify: `apps/api/src/routes/providers-status.ts`
- Test: `apps/api/test/routes/rbac-settings-enforcement.test.ts` (new)

**Step 1: Write the failing test**

Create `apps/api/test/routes/rbac-settings-enforcement.test.ts`:

```ts
it("returns 403 when permission exists but setting disabled", async () => {
  const res = await app.inject({ method: "POST", url: "/v1/images/generations", headers: authHeaders });
  expect(res.statusCode).toBe(403);
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @hive/api test apps/api/test/routes/rbac-settings-enforcement.test.ts`
Expected: FAIL because routes do not evaluate settings gates.

**Step 3: Write minimal implementation**
- Route-level declarative checks: required permission + required setting key.
- Preserve existing response shapes and headers.
- Keep `/v1/providers/status` sanitized and `/internal` protected.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @hive/api test apps/api/test/routes/rbac-settings-enforcement.test.ts`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/api/src/routes/chat-completions.ts apps/api/src/routes/images-generations.ts apps/api/src/routes/usage.ts apps/api/src/routes/credits-balance.ts apps/api/src/routes/payment-intents.ts apps/api/src/routes/payment-demo-confirm.ts apps/api/src/routes/providers-status.ts apps/api/test/routes/rbac-settings-enforcement.test.ts
git commit -m "feat(api): enforce permission and user-setting gates on protected routes"
```

### Task 7: Add user settings management endpoints

**Files:**
- Modify: `apps/api/src/routes/users.ts`
- Test: `apps/api/test/routes/users-settings-routes.test.ts` (new)

**Step 1: Write the failing test**

Create `apps/api/test/routes/users-settings-routes.test.ts` for:
- `GET /v1/users/settings`
- `PATCH /v1/users/settings` validates allowed keys
- `apiEnabled=false` blocks key creation usage flow

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @hive/api test apps/api/test/routes/users-settings-routes.test.ts`
Expected: FAIL because routes do not exist.

**Step 3: Write minimal implementation**
- Add read/update settings endpoints with allowlist validation.
- Return deterministic shape for UI.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @hive/api test apps/api/test/routes/users-settings-routes.test.ts`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/api/src/routes/users.ts apps/api/test/routes/users-settings-routes.test.ts
git commit -m "feat(api): add user settings read and update endpoints"
```

### Task 8: Add 2FA phase-1 endpoints and state model

**Files:**
- Create: `apps/api/src/routes/two-factor.ts`
- Modify: `apps/api/src/routes/index.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Test: `apps/api/test/routes/two-factor-routes.test.ts` (new)

**Step 1: Write the failing test**

Create `apps/api/test/routes/two-factor-routes.test.ts` for:
- enroll init returns challenge metadata.
- enroll verify enables `twoFactorEnabled`.
- challenge verify returns success/failure.

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @hive/api test apps/api/test/routes/two-factor-routes.test.ts`
Expected: FAIL because endpoints/services do not exist.

**Step 3: Write minimal implementation**
- Add phase-1 routes and storage model.
- Do not enforce globally yet.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @hive/api test apps/api/test/routes/two-factor-routes.test.ts`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/api/src/routes/two-factor.ts apps/api/src/routes/index.ts apps/api/src/runtime/services.ts apps/api/test/routes/two-factor-routes.test.ts
git commit -m "feat(api): add phase-1 two-factor enrollment and challenge routes"
```

### Task 9: Add sensitive-action 2FA enforcement toggles

**Files:**
- Modify: `apps/api/src/routes/users.ts`
- Modify: `apps/api/src/routes/payment-intents.ts`
- Modify: `apps/api/src/runtime/services.ts`
- Test: `apps/api/test/routes/two-factor-enforcement.test.ts` (new)

**Step 1: Write the failing test**

Create `apps/api/test/routes/two-factor-enforcement.test.ts`:

```ts
it("blocks api-key creation when 2fa is required but not recently verified", async () => {
  expect(response.statusCode).toBe(403);
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @hive/api test apps/api/test/routes/two-factor-enforcement.test.ts`
Expected: FAIL because enforcement hook is missing.

**Step 3: Write minimal implementation**
- Add optional enforcement hook keyed by setting/env and operation type.
- Apply to API-key management and selected billing operations.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @hive/api test apps/api/test/routes/two-factor-enforcement.test.ts`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/api/src/routes/users.ts apps/api/src/routes/payment-intents.ts apps/api/src/runtime/services.ts apps/api/test/routes/two-factor-enforcement.test.ts
git commit -m "feat(api): add optional 2fa enforcement for sensitive actions"
```

### Task 10: Web integration for Google login and settings UI

**Files:**
- Modify: `apps/web/src/app/chat/page.tsx`
- Modify: `apps/web/src/app/billing/page.tsx`
- Create: `apps/web/src/features/auth/google-login-button.tsx`
- Create: `apps/web/src/features/settings/user-settings-panel.tsx`
- Test: `apps/web/test/google-login-ui.test.tsx` (new)
- Test: `apps/web/test/user-settings-panel.test.tsx` (new)

**Step 1: Write the failing tests**

Add UI tests asserting:
- Google login button renders and points to auth start endpoint.
- Settings toggles render (`apiEnabled`, `generateImage`, `twoFactorEnabled`) and call update handler.

**Step 2: Run tests to verify they fail**

Run: `pnpm --filter @hive/web test test/google-login-ui.test.tsx test/user-settings-panel.test.tsx`
Expected: FAIL because components do not exist.

**Step 3: Write minimal implementation**
- Add Google login CTA and callback handling state.
- Add settings panel with toggles and optimistic update UX.

**Step 4: Run tests to verify they pass**

Run: `pnpm --filter @hive/web test test/google-login-ui.test.tsx test/user-settings-panel.test.tsx`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/web/src/app/chat/page.tsx apps/web/src/app/billing/page.tsx apps/web/src/features/auth/google-login-button.tsx apps/web/src/features/settings/user-settings-panel.tsx apps/web/test/google-login-ui.test.tsx apps/web/test/user-settings-panel.test.tsx
git commit -m "feat(web): add google login and user settings controls"
```

### Task 11: Documentation and full verification

**Files:**
- Modify: `README.md`
- Modify: `docs/README.md`
- Create: `docs/runbooks/auth-rbac-settings-2fa.md`

**Step 1: Write doc expectation test (optional lightweight check)**

Add simple expectation in an existing test file for presence of new auth env var references.

**Step 2: Run to verify fail**

Run: `pnpm --filter @hive/api test`
Expected: FAIL before docs updates if test is added.

**Step 3: Write minimal documentation updates**
- Add env vars and endpoint docs.
- Add runbook for seeding roles/permissions and troubleshooting.

**Step 4: Run full verification**

Run:
- `pnpm --filter @hive/api test`
- `pnpm --filter @hive/api build`
- `pnpm --filter @hive/web test`
- `pnpm --filter @hive/web build`

Expected:
- All test suites pass.
- API and web builds succeed.

**Step 5: Commit**

```bash
git add README.md docs/README.md docs/runbooks/auth-rbac-settings-2fa.md
git commit -m "docs(auth): add google oauth, rbac, settings, and 2fa runbook"
```
