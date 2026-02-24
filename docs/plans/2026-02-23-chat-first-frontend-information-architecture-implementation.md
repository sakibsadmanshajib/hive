# Chat-First Frontend Information Architecture Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make chat the default authenticated landing page, introduce peer top-right destinations for Developer Panel and Settings, and apply the approved Option A visual redesign across core web routes.

**Architecture:** Keep existing feature modules and API contracts, but remap route ownership: chat on `/`, developer workflows on `/developer`, and account workflows on `/settings`. Refactor shared shell/header tokens first, then migrate page responsibilities incrementally behind test coverage. Preserve existing auth gate behavior and responsive patterns.

**Tech Stack:** Next.js App Router, React 19, TypeScript, Tailwind CSS, Vitest + Testing Library

---

Use @superpowers/test-driven-development for implementation steps and @superpowers/verification-before-completion before any done claim.

### Task 1: Add web test harness for route/shell behavior

**Files:**
- Create: `apps/web/test/setup.ts`
- Create: `apps/web/test/navigation-shell.test.tsx`
- Modify: `apps/web/vitest.config.ts`
- Modify: `apps/web/package.json`

**Step 1: Write the failing test**

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { AppHeader } from "../src/components/layout/app-header";

describe("AppHeader", () => {
  it("shows Developer Panel and Settings actions", () => {
    render(<AppHeader />);
    expect(screen.getByRole("link", { name: /developer panel/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /settings/i })).toBeInTheDocument();
  });
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @bd-ai-gateway/web test -- navigation-shell.test.tsx`
Expected: FAIL because links are not present and test environment setup is incomplete.

**Step 3: Write minimal implementation for test environment**

```ts
// apps/web/test/setup.ts
import "@testing-library/jest-dom/vitest";
```

```ts
// apps/web/vitest.config.ts (test block)
test: {
  include: ["test/**/*.test.ts", "test/**/*.test.tsx"],
  environment: "jsdom",
  setupFiles: ["test/setup.ts"],
}
```

**Step 4: Run test to verify harness works and assertion still fails**

Run: `pnpm --filter @bd-ai-gateway/web test -- navigation-shell.test.tsx`
Expected: FAIL only on missing header links (environment errors resolved).

**Step 5: Commit**

```bash
git add apps/web/test/setup.ts apps/web/test/navigation-shell.test.tsx apps/web/vitest.config.ts apps/web/package.json
git commit -m "test(web): add shell navigation test harness"
```

### Task 2: Refactor header and sidebar IA for peer destinations

**Files:**
- Modify: `apps/web/src/components/layout/app-header.tsx`
- Modify: `apps/web/src/components/layout/app-sidebar.tsx`
- Modify: `apps/web/src/components/layout/app-shell.tsx`
- Test: `apps/web/test/navigation-shell.test.tsx`

**Step 1: Write the failing tests for IA behavior**

```tsx
it("keeps chat as primary nav and removes old top-level Billing/Auth links", () => {
  render(<AppSidebar />);
  expect(screen.getByRole("link", { name: /chat/i })).toBeInTheDocument();
  expect(screen.queryByRole("link", { name: /^billing$/i })).not.toBeInTheDocument();
  expect(screen.queryByRole("link", { name: /^auth$/i })).not.toBeInTheDocument();
});
```

**Step 2: Run tests to verify failures**

Run: `pnpm --filter @bd-ai-gateway/web test -- navigation-shell.test.tsx`
Expected: FAIL because legacy links still exist.

**Step 3: Implement minimal IA update**

```tsx
// header action examples
<Link href="/developer">Developer Panel</Link>
<Link href="/settings">Settings</Link>
```

```tsx
// sidebar nav examples
const navItems = [{ href: "/", label: "Chat" }];
```

**Step 4: Run tests to verify pass**

Run: `pnpm --filter @bd-ai-gateway/web test -- navigation-shell.test.tsx`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/web/src/components/layout/app-header.tsx apps/web/src/components/layout/app-sidebar.tsx apps/web/src/components/layout/app-shell.tsx apps/web/test/navigation-shell.test.tsx
git commit -m "feat(web): make developer and settings peer header actions"
```

### Task 3: Make chat route the default landing page

**Files:**
- Create: `apps/web/src/features/chat/components/chat-workspace.tsx`
- Modify: `apps/web/src/app/page.tsx`
- Modify: `apps/web/src/app/chat/page.tsx`
- Test: `apps/web/test/navigation-shell.test.tsx`

**Step 1: Write the failing test for default chat landing**

```tsx
it("renders chat workspace on root route", async () => {
  const module = await import("../src/app/page");
  expect(module.default).toBeTypeOf("function");
});
```

**Step 2: Run test to verify current behavior gap**

Run: `pnpm --filter @bd-ai-gateway/web test -- navigation-shell.test.tsx`
Expected: FAIL once assertions check for chat-specific structure instead of marketing cards.

**Step 3: Implement minimal route remap**

```tsx
// app/page.tsx
export { default } from "./chat/page";
```

Then extract shared chat page JSX into `chat-workspace.tsx` and have both routes render it during migration to avoid breakage.

**Step 4: Run tests to verify pass**

Run: `pnpm --filter @bd-ai-gateway/web test -- navigation-shell.test.tsx`
Expected: PASS with root route mapped to chat.

**Step 5: Commit**

```bash
git add apps/web/src/features/chat/components/chat-workspace.tsx apps/web/src/app/page.tsx apps/web/src/app/chat/page.tsx apps/web/test/navigation-shell.test.tsx
git commit -m "feat(web): make chat workspace default route"
```

### Task 4: Introduce Developer Panel route and migrate developer widgets

**Files:**
- Create: `apps/web/src/app/developer/page.tsx`
- Create: `apps/web/src/features/developer/components/developer-shell.tsx`
- Modify: `apps/web/src/app/billing/page.tsx`
- Modify: `apps/web/src/features/billing/components/usage-cards.tsx`
- Modify: `apps/web/src/features/billing/components/topup-panel.tsx`
- Test: `apps/web/test/navigation-shell.test.tsx`

**Step 1: Write failing tests for new route affordances**

```tsx
it("exposes Developer Panel route link in header", () => {
  render(<AppHeader />);
  expect(screen.getByRole("link", { name: /developer panel/i })).toHaveAttribute("href", "/developer");
});
```

**Step 2: Run test to verify fail**

Run: `pnpm --filter @bd-ai-gateway/web test -- navigation-shell.test.tsx`
Expected: FAIL until route link and migrated page exist.

**Step 3: Implement minimal developer page composition**

```tsx
// app/developer/page.tsx
<DeveloperShell>
  <UsageCards ... />
  <TopUpPanel ... />
  {/* API key management card */}
</DeveloperShell>
```

Move developer-oriented content out of `billing/page.tsx` into the new route.

**Step 4: Run tests to verify pass**

Run: `pnpm --filter @bd-ai-gateway/web test -- navigation-shell.test.tsx`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/web/src/app/developer/page.tsx apps/web/src/features/developer/components/developer-shell.tsx apps/web/src/app/billing/page.tsx apps/web/src/features/billing/components/usage-cards.tsx apps/web/src/features/billing/components/topup-panel.tsx apps/web/test/navigation-shell.test.tsx
git commit -m "feat(web): add developer panel and migrate developer workflows"
```

### Task 5: Introduce Settings route and migrate account/billing settings

**Files:**
- Create: `apps/web/src/app/settings/page.tsx`
- Create: `apps/web/src/features/settings/components/settings-shell.tsx`
- Modify: `apps/web/src/features/settings/user-settings-panel.tsx`
- Modify: `apps/web/src/app/billing/page.tsx`
- Modify: `apps/web/src/app/auth/page.tsx`
- Test: `apps/web/test/navigation-shell.test.tsx`

**Step 1: Write failing tests for settings navigation**

```tsx
it("exposes Settings route link in header", () => {
  render(<AppHeader />);
  expect(screen.getByRole("link", { name: /settings/i })).toHaveAttribute("href", "/settings");
});
```

**Step 2: Run test to verify fail**

Run: `pnpm --filter @bd-ai-gateway/web test -- navigation-shell.test.tsx`
Expected: FAIL until settings route and link are wired.

**Step 3: Implement minimal settings page composition**

```tsx
// app/settings/page.tsx
<SettingsShell>
  <UserSettingsPanel ... />
  {/* profile and billing method sections */}
</SettingsShell>
```

Keep `billing/page.tsx` as a temporary redirect or compatibility screen to prevent dead links.

**Step 4: Run tests to verify pass**

Run: `pnpm --filter @bd-ai-gateway/web test -- navigation-shell.test.tsx`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/web/src/app/settings/page.tsx apps/web/src/features/settings/components/settings-shell.tsx apps/web/src/features/settings/user-settings-panel.tsx apps/web/src/app/billing/page.tsx apps/web/src/app/auth/page.tsx apps/web/test/navigation-shell.test.tsx
git commit -m "feat(web): add settings workspace and migrate account controls"
```

### Task 6: Apply Option A visual redesign tokens and component polish

**Files:**
- Modify: `apps/web/src/app/globals.css`
- Modify: `apps/web/src/components/layout/app-shell.tsx`
- Modify: `apps/web/src/features/chat/components/chat-shell.tsx`
- Modify: `apps/web/src/features/chat/components/message-list.tsx`
- Modify: `apps/web/src/features/chat/components/message-composer.tsx`
- Modify: `apps/web/src/features/billing/components/billing-shell.tsx`
- Test: `apps/web/test/navigation-shell.test.tsx`

**Step 1: Write failing tests for key style hooks/classes**

```tsx
it("uses upgraded shell class hooks for editorial layout", () => {
  render(<AppShell><div>content</div></AppShell>);
  expect(document.querySelector("[data-app-shell='editorial']")).toBeInTheDocument();
});
```

**Step 2: Run tests to verify fail**

Run: `pnpm --filter @bd-ai-gateway/web test -- navigation-shell.test.tsx`
Expected: FAIL because hooks/classes are not implemented.

**Step 3: Implement minimal visual system updates**

```tsx
// app-shell root marker
<div data-app-shell="editorial" className="...">
```

```css
/* globals.css token update example */
:root {
  --background: 42 38% 97%;
  --card: 0 0% 100%;
  --primary: 199 89% 32%;
}
```

**Step 4: Run tests to verify pass**

Run: `pnpm --filter @bd-ai-gateway/web test -- navigation-shell.test.tsx`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/web/src/app/globals.css apps/web/src/components/layout/app-shell.tsx apps/web/src/features/chat/components/chat-shell.tsx apps/web/src/features/chat/components/message-list.tsx apps/web/src/features/chat/components/message-composer.tsx apps/web/src/features/billing/components/billing-shell.tsx apps/web/test/navigation-shell.test.tsx
git commit -m "feat(web): apply option-a editorial fintech visual redesign"
```

### Task 7: Verification and docs alignment

**Files:**
- Modify: `README.md`
- Modify: `docs/README.md`
- Modify: `docs/plans/README.md`

**Step 1: Write failing documentation checklist (manual test doc block)**

```md
- [ ] README route map reflects chat-first IA
- [ ] docs index references developer/settings surfaces
- [ ] plan index references implementation plan
```

**Step 2: Run verification commands before doc edits**

Run: `pnpm --filter @bd-ai-gateway/web test`
Expected: PASS.

Run: `pnpm --filter @bd-ai-gateway/web build`
Expected: PASS.

**Step 3: Implement minimal docs updates**

```md
Default app flow: `/` chat, `/developer` developer tools, `/settings` account settings.
```

**Step 4: Re-run verification**

Run: `pnpm --filter @bd-ai-gateway/web test && pnpm --filter @bd-ai-gateway/web build`
Expected: PASS.

**Step 5: Commit**

```bash
git add README.md docs/README.md docs/plans/README.md docs/plans/2026-02-23-chat-first-frontend-information-architecture-implementation.md
git commit -m "docs(web): align documentation with chat-first information architecture"
```

## Final Verification Checklist

- `pnpm --filter @bd-ai-gateway/web test`
- `pnpm --filter @bd-ai-gateway/web build`
- Manual route smoke test: `/auth`, `/`, `/developer`, `/settings`
- Responsive checks for header actions and chat sidebar/drawer
- Confirm no API contract or backend file changes were introduced
