# Auth-First Chat Entry Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Move auth to a dedicated `/auth` screen with Google sign-in, redirect unauthenticated users away from `/chat`, and remove in-chat session setup UI.

**Architecture:** Create a lightweight client auth store (localStorage-backed) under `features/auth`, build a focused `/auth` route for register/login/Google entry, and gate chat rendering based on stored auth state. Keep API contracts unchanged and reuse existing chat reducer/components.

**Tech Stack:** Next.js app router, React 19 hooks, TypeScript, Tailwind UI components, Vitest + Testing Library.

---

### Task 1: Add auth state persistence utilities

**Files:**
- Create: `apps/web/src/features/auth/auth-session.ts`
- Test: `apps/web/test/auth-session.test.ts`

**Step 1: Write the failing test**

```ts
import { describe, expect, it } from "vitest";
import { clearAuthSession, readAuthSession, writeAuthSession } from "../src/features/auth/auth-session";

describe("auth session", () => {
  it("persists and reads session payload", () => {
    writeAuthSession({ apiKey: "sk_test", email: "demo@example.com", name: "Demo" });
    expect(readAuthSession()?.apiKey).toBe("sk_test");
  });

  it("clears persisted session", () => {
    writeAuthSession({ apiKey: "sk_test", email: "demo@example.com" });
    clearAuthSession();
    expect(readAuthSession()).toBeNull();
  });
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @hive/web test apps/web/test/auth-session.test.ts`
Expected: FAIL because module does not exist yet.

**Step 3: Write minimal implementation**

```ts
export type AuthSession = { apiKey: string; email: string; name?: string };

const AUTH_STORAGE_KEY = "bdai.auth.session";

export function readAuthSession(): AuthSession | null {
  if (typeof window === "undefined") return null;
  const raw = window.localStorage.getItem(AUTH_STORAGE_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as AuthSession;
  } catch {
    return null;
  }
}

export function writeAuthSession(session: AuthSession): void {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(AUTH_STORAGE_KEY, JSON.stringify(session));
}

export function clearAuthSession(): void {
  if (typeof window === "undefined") return;
  window.localStorage.removeItem(AUTH_STORAGE_KEY);
}
```

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @hive/web test apps/web/test/auth-session.test.ts`
Expected: PASS

**Step 5: Commit**

```bash
git add apps/web/src/features/auth/auth-session.ts apps/web/test/auth-session.test.ts
git commit -m "feat(web): add persisted auth session helpers"
```

### Task 2: Build dedicated auth page with register/login/Google

**Files:**
- Create: `apps/web/src/app/auth/page.tsx`
- Test: `apps/web/test/auth-page.test.tsx`

**Step 1: Write the failing test**

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import AuthPage from "../src/app/auth/page";

describe("AuthPage", () => {
  it("renders login/register surfaces and google sign in", () => {
    render(<AuthPage />);
    expect(screen.getByText("Login")).toBeInTheDocument();
    expect(screen.getByText("Register")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /continue with google/i })).toBeInTheDocument();
  });
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @hive/web test apps/web/test/auth-page.test.tsx`
Expected: FAIL because route does not exist.

**Step 3: Write minimal implementation**

Implement `apps/web/src/app/auth/page.tsx`:
- register form (name/email/password)
- login form (email/password)
- `GoogleLoginButton`
- calls `POST /v1/users/register` and `POST /v1/users/login`
- on success writes session via `writeAuthSession(...)`
- redirects to `/chat` via Next router
- shows auth error message/toast feedback

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @hive/web test apps/web/test/auth-page.test.tsx`
Expected: PASS

**Step 5: Commit**

```bash
git add apps/web/src/app/auth/page.tsx apps/web/test/auth-page.test.tsx
git commit -m "feat(web): add dedicated auth entry page"
```

### Task 3: Refactor chat page to require auth and remove session setup

**Files:**
- Modify: `apps/web/src/app/chat/page.tsx`
- Modify: `apps/web/src/features/chat/use-chat-session.ts`
- Test: `apps/web/test/chat-auth-gate.test.tsx`

**Step 1: Write the failing test**

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import ChatPage from "../src/app/chat/page";

describe("Chat auth gate", () => {
  it("does not render session setup UI", () => {
    render(<ChatPage />);
    expect(screen.queryByText("Session setup")).not.toBeInTheDocument();
  });
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @hive/web test apps/web/test/chat-auth-gate.test.tsx`
Expected: FAIL because session setup card is still rendered.

**Step 3: Write minimal implementation**

- Update `use-chat-session.ts`:
  - initialize API key from `readAuthSession()`
  - keep message/chat logic unchanged
- Update `chat/page.tsx`:
  - remove register/login/api-key card
  - if missing auth session/API key, redirect to `/auth`
  - render chat shell + messages + composer only

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @hive/web test apps/web/test/chat-auth-gate.test.tsx`
Expected: PASS

**Step 5: Commit**

```bash
git add apps/web/src/app/chat/page.tsx apps/web/src/features/chat/use-chat-session.ts apps/web/test/chat-auth-gate.test.tsx
git commit -m "refactor(web): gate chat behind auth and remove setup panel"
```

### Task 4: Update navigation and smoke coverage

**Files:**
- Modify: `apps/web/src/components/layout/app-sidebar.tsx`
- Modify: `apps/web/test/app-shell.test.tsx` (or create `apps/web/test/auth-routing-nav.test.tsx`)

**Step 1: Write the failing test**

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AppSidebar } from "../src/components/layout/app-sidebar";

describe("AppSidebar", () => {
  it("includes Auth entry link", () => {
    render(<AppSidebar />);
    expect(screen.getByRole("link", { name: "Auth" })).toBeInTheDocument();
  });
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @hive/web test apps/web/test/auth-routing-nav.test.tsx`
Expected: FAIL because link is missing.

**Step 3: Write minimal implementation**

- Add `Auth` nav link to sidebar to make entry explicit.
- Keep existing Home/Chat/Billing links.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @hive/web test apps/web/test/auth-routing-nav.test.tsx`
Expected: PASS

**Step 5: Commit**

```bash
git add apps/web/src/components/layout/app-sidebar.tsx apps/web/test/auth-routing-nav.test.tsx
git commit -m "chore(web): add auth route navigation entry"
```

### Task 5: Full verification and docs index update

**Files:**
- Modify: `docs/README.md`

**Step 1: Write failing doc assertion (optional snapshot test) or proceed with docs update**

```md
Add links to new auth-first design + implementation docs in docs index.
```

**Step 2: Run targeted checks**

Run: `pnpm --filter @hive/web test`
Expected: PASS

**Step 3: Run build**

Run: `pnpm --filter @hive/web build`
Expected: PASS

**Step 4: Runtime smoke checks**

Run:
- `curl -i -s http://127.0.0.1:3000/auth`
- `curl -i -s http://127.0.0.1:3000/chat`

Expected:
- `/auth` returns 200 HTML page
- `/chat` no longer includes session setup markup

**Step 5: Commit**

```bash
git add docs/README.md
git commit -m "docs(plans): index auth-first chat entry docs"
```
