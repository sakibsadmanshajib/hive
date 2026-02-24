# Web E2E Smoke Coverage (Auth -> Chat -> Billing) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add Playwright smoke e2e coverage for unauth/auth/chat/billing flows (including chat and billing failure states) with repeatable local and CI commands.

**Architecture:** Add a Playwright test runner in `apps/web` and run smoke specs against the Docker full stack baseline. Keep tests deterministic by using API-assisted auth setup and stable selectors, while preserving current API contracts and user-facing behavior. Add one CI path dedicated to web smoke e2e so regular unit/build checks remain fast.

**Tech Stack:** Next.js 15, TypeScript, Playwright, pnpm workspaces, GitHub Actions, Docker Compose.

---

### Task 1: Add Playwright runner and command wiring

**Files:**
- Modify: `apps/web/package.json`
- Create: `apps/web/playwright.config.ts`
- Modify: `pnpm-lock.yaml`

**Step 1: Write the failing test command contract**

Add expected scripts in `apps/web/package.json` (this is the contract the next step will verify):

```json
{
  "scripts": {
    "test:e2e": "playwright test",
    "test:e2e:headed": "playwright test --headed"
  }
}
```

**Step 2: Run command to verify it fails before dependency/config**

Run: `pnpm --filter @bd-ai-gateway/web test:e2e`
Expected: FAIL with missing `playwright` binary and/or missing config/spec files.

**Step 3: Write minimal Playwright config**

Create `apps/web/playwright.config.ts`:

```ts
import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  retries: process.env.CI ? 1 : 0,
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://127.0.0.1:3000",
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  reporter: [["list"], ["html", { open: "never" }]],
});
```

**Step 4: Install dependency and verify command is executable**

Run: `pnpm --filter @bd-ai-gateway/web add -D @playwright/test`

Then run: `pnpm --filter @bd-ai-gateway/web test:e2e`
Expected: FAIL with "No tests found" (runner is now configured correctly).

**Step 5: Commit**

```bash
git add apps/web/package.json apps/web/playwright.config.ts pnpm-lock.yaml
git commit -m "test(web): add playwright e2e runner scaffolding"
```

### Task 2: Add deterministic smoke test fixture and first failing spec

**Files:**
- Create: `apps/web/e2e/fixtures/auth.ts`
- Create: `apps/web/e2e/smoke-auth-chat-billing.spec.ts`

**Step 1: Write failing smoke spec skeleton**

Create the initial test file with one intentionally failing expectation to establish TDD baseline:

```ts
import { test, expect } from "@playwright/test";

test("unauthenticated root is redirected to auth", async ({ page }) => {
  await page.goto("/");
  await expect(page).toHaveURL(/\/auth/);
  await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible();
});
```

**Step 2: Run targeted test to verify current failure signal**

Run: `pnpm --filter @bd-ai-gateway/web test:e2e -- smoke-auth-chat-billing.spec.ts`
Expected: FAIL until fixture setup and runtime assumptions are completed.

**Step 3: Add API-assisted auth helper fixture**

Create `apps/web/e2e/fixtures/auth.ts`:

```ts
import type { Page } from "@playwright/test";

type SessionSeed = { apiKey: string; email: string; name?: string };

export async function seedAuthSession(page: Page, seed: SessionSeed) {
  await page.addInitScript((data) => {
    localStorage.setItem("bdag.auth.session", JSON.stringify(data));
  }, {
    apiKey: seed.apiKey,
    email: seed.email,
    name: seed.name,
  });
}
```

**Step 4: Update spec to use helper and pass first scenario**

Use helper in spec and assert auth-path behavior deterministically.

**Step 5: Commit**

```bash
git add apps/web/e2e/fixtures/auth.ts apps/web/e2e/smoke-auth-chat-billing.spec.ts
git commit -m "test(web): add smoke e2e fixture and baseline auth scenario"
```

### Task 3: Implement full smoke coverage (happy + failure paths)

**Files:**
- Modify: `apps/web/e2e/smoke-auth-chat-billing.spec.ts`
- Optional Modify (only if selectors are too brittle):
  - `apps/web/src/app/auth/page.tsx`
  - `apps/web/src/app/chat/page.tsx`
  - `apps/web/src/app/billing/page.tsx`
  - `apps/web/src/components/layout/app-sidebar.tsx`

**Step 1: Write failing tests for remaining required scenarios**

Add tests for:

```ts
test("login/register happy path reaches chat", async ({ page }) => {
  // register via UI or API-assisted setup
  // assert chat composer visible
});

test("chat failure path shows error messaging", async ({ page }) => {
  // route /v1/chat/completions to 500 with { error: "chat failed" }
  // assert failure message is visible
});

test("billing is reachable and billing failure messaging is visible", async ({ page }) => {
  // navigate to /billing
  // force /v1/payments/intents 500
  // assert billing status message reflects failure
});
```

**Step 2: Run targeted spec to verify failures**

Run: `pnpm --filter @bd-ai-gateway/web test:e2e -- smoke-auth-chat-billing.spec.ts`
Expected: FAIL on newly added scenarios before stabilization.

**Step 3: Add minimal selector hardening only where needed**

If role/text selectors prove unstable, add `data-testid` attributes minimally (no visual changes), for example:

```tsx
<Button data-testid="chat-send-button" ...>
<Input data-testid="billing-api-key" ...>
<nav data-testid="primary-sidebar" ...>
```

**Step 4: Run targeted spec until all smoke cases pass**

Run: `pnpm --filter @bd-ai-gateway/web test:e2e -- smoke-auth-chat-billing.spec.ts`
Expected: PASS for all required smoke cases.

**Step 5: Commit**

```bash
git add apps/web/e2e/smoke-auth-chat-billing.spec.ts apps/web/src/app/auth/page.tsx apps/web/src/app/chat/page.tsx apps/web/src/app/billing/page.tsx apps/web/src/components/layout/app-sidebar.tsx
git commit -m "test(web): cover auth chat billing smoke happy and failure paths"
```

### Task 4: Add CI-friendly execution path

**Files:**
- Create: `.github/workflows/web-e2e-smoke.yml`
- Optional Modify (if reusing existing workflow): `.github/workflows/ci.yml`

**Step 1: Write failing CI contract in workflow draft**

Create job skeleton expecting these steps:

```yaml
name: Web E2E Smoke
on:
  pull_request:
    paths:
      - "apps/web/**"
      - "apps/api/**"
      - "docker-compose.yml"
      - ".github/workflows/web-e2e-smoke.yml"
```

**Step 2: Run local workflow-equivalent commands and verify baseline gaps**

Run locally in sequence:
- `docker compose up --build -d`
- `docker compose ps`
- `pnpm --filter @bd-ai-gateway/web build`
- `pnpm --filter @bd-ai-gateway/web test:e2e`

Expected: Identify missing env/setup pieces before finalizing workflow.

**Step 3: Implement minimal complete workflow**

Include:
- checkout, pnpm/node setup
- dependency install
- docker compose up
- health check for API and web readiness
- e2e run command
- Playwright report upload on failure

**Step 4: Validate command parity locally**

Run: `pnpm --filter @bd-ai-gateway/web test:e2e`
Expected: PASS when stack is healthy.

**Step 5: Commit**

```bash
git add .github/workflows/web-e2e-smoke.yml .github/workflows/ci.yml
git commit -m "ci(web): add docker-backed playwright smoke workflow"
```

### Task 5: Document usage and verification

**Files:**
- Modify: `README.md`
- Modify: `docs/README.md`
- Create or Modify: `docs/runbooks/web-e2e-smoke.md`

**Step 1: Write failing docs checklist**

Add checklist items to satisfy:
- local prerequisites
- exact command(s)
- expected coverage scope
- troubleshooting notes for Docker readiness

**Step 2: Verify docs are currently missing this exact flow**

Run: `gh issue view 16 --repo sakibsadmanshajib/hive --json body`
Expected: Confirms acceptance asks for command + local/CI coverage.

**Step 3: Add minimal docs updates**

Document examples:

```bash
docker compose up --build -d
pnpm --filter @bd-ai-gateway/web test:e2e
```

and expected smoke scope bullets.

**Step 4: Run validation commands before completion claim**

Run:
- `pnpm --filter @bd-ai-gateway/web test:e2e`
- `pnpm --filter @bd-ai-gateway/api test`
- `pnpm --filter @bd-ai-gateway/api build`

Expected: PASS before marking complete.

**Step 5: Commit**

```bash
git add README.md docs/README.md docs/runbooks/web-e2e-smoke.md
git commit -m "docs(web): document smoke e2e command and operational checks"
```

### Task 6: Final verification and PR readiness

**Files:**
- Modify: `docs/plans/2026-02-24-web-e2e-smoke-auth-chat-billing-design.md` (only if implementation deviated)

**Step 1: Run full web verification bundle**

Run:
- `pnpm --filter @bd-ai-gateway/web test`
- `pnpm --filter @bd-ai-gateway/web build`
- `pnpm --filter @bd-ai-gateway/web test:e2e`

Expected: PASS.

**Step 2: Re-run required API gates from AGENTS.md**

Run:
- `pnpm --filter @bd-ai-gateway/api test`
- `pnpm --filter @bd-ai-gateway/api build`

Expected: PASS.

**Step 3: Confirm issue acceptance criteria mapping**

Create a short checklist in PR description mapping each criterion to:
- command,
- spec path,
- CI workflow path.

**Step 4: Update design doc only if reality changed**

If implementation differs from design decisions, add a small "Implementation Notes" section.

**Step 5: Commit**

```bash
git add docs/plans/2026-02-24-web-e2e-smoke-auth-chat-billing-design.md
git commit -m "docs(plans): align smoke e2e design notes with implementation"
```
