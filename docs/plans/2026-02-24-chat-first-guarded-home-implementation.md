# Chat-First Guarded Home Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Deliver a chat-first web flow where `/` is the guarded chat home, unauthenticated users are redirected to `/auth`, and authenticated users get a modern ChatGPT-like workspace with avatar menu access to Settings, Developer Panel, and Billing.

**Architecture:** Keep the existing Next.js app-router structure but switch route responsibility: `app/page.tsx` becomes guarded chat entry, and `app/chat/page.tsx` becomes a redirect shim. Introduce a dedicated chat workspace shell component with left rail + top-right account menu, and unify auth/session consumption across chat and billing to remove manual API key primary path.

**Tech Stack:** Next.js 15 app router, React 19, TypeScript, Tailwind CSS, Radix UI primitives, Vitest + Testing Library.

---

## Implementation Tasks

### Task 1: Guarded Root Routing

**Files:**
- Modify: `apps/web/src/app/page.tsx`
- Modify: `apps/web/src/app/chat/page.tsx`
- Test: `apps/web/test/chat-auth-gate.test.ts`

**Step 1: Write the failing test for root guard behavior**

```tsx
it("redirects unauthenticated users from root to /auth", () => {
  // mock readAuthSession => null
  // render root page
  // expect router.push('/auth')
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @bd-ai-gateway/web test -- test/chat-auth-gate.test.ts`
Expected: FAIL because `/` is currently marketing/home content.

**Step 3: Implement root guarded chat entry**

```tsx
export default function HomePage() {
  const authSession = readAuthSession();
  useEffect(() => {
    if (!authSession?.apiKey) router.push("/auth");
  }, [authSession?.apiKey, router]);
  if (!authSession?.apiKey) return null;
  return <ChatWorkspacePage />;
}
```

**Step 4: Add `/chat` -> `/` redirect behavior**

```tsx
export default function ChatLegacyRoute() {
  const router = useRouter();
  useEffect(() => { router.replace("/"); }, [router]);
  return null;
}
```

**Step 5: Re-run test to verify pass**

Run: `pnpm --filter @bd-ai-gateway/web test -- test/chat-auth-gate.test.ts`
Expected: PASS.

**Step 6: Commit**

```bash
git add apps/web/src/app/page.tsx apps/web/src/app/chat/page.tsx apps/web/test/chat-auth-gate.test.ts
git commit -m "feat(web): make root guarded chat entry"
```

### Task 2: Chat Workspace Shell (Left Rail + Top-Right Profile Menu)

**Files:**
- Create: `apps/web/src/features/chat/components/chat-workspace-shell.tsx`
- Modify: `apps/web/src/app/page.tsx`
- Modify: `apps/web/src/features/chat/components/conversation-list.tsx`
- Create: `apps/web/src/features/account/components/profile-menu.tsx`
- Test: `apps/web/test/chat-mobile-layout.test.tsx`
- Test: `apps/web/test/app-shell.test.tsx`

**Step 1: Write failing layout/menu tests**

```tsx
it("shows left conversation rail on desktop");
it("shows profile avatar menu with Settings, Developer Panel, Billing, Log out");
it("collapses rail into mobile sheet trigger");
```

**Step 2: Run tests to verify they fail**

Run: `pnpm --filter @bd-ai-gateway/web test -- test/chat-mobile-layout.test.tsx test/app-shell.test.tsx`
Expected: FAIL because profile menu/shell does not yet exist.

**Step 3: Implement chat workspace shell**

```tsx
<section className="grid h-[calc(100vh-...)] grid-cols-[280px_1fr]">
  <aside>{/* conversation rail */}</aside>
  <main>{/* timeline + composer */}</main>
</section>
```

**Step 4: Implement profile avatar dropdown menu**

```tsx
<DropdownMenu>
  <DropdownMenuTrigger><Avatar /></DropdownMenuTrigger>
  <DropdownMenuContent>
    <DropdownMenuItem>Settings</DropdownMenuItem>
    <DropdownMenuItem>Developer Panel</DropdownMenuItem>
    <DropdownMenuItem>Billing</DropdownMenuItem>
    <DropdownMenuItem>Log out</DropdownMenuItem>
  </DropdownMenuContent>
</DropdownMenu>
```

**Step 5: Wire shell into root chat page**

Run chat timeline/composer inside `chat-workspace-shell.tsx`.

**Step 6: Re-run tests to verify pass**

Run: `pnpm --filter @bd-ai-gateway/web test -- test/chat-mobile-layout.test.tsx test/app-shell.test.tsx`
Expected: PASS.

**Step 7: Commit**

```bash
git add apps/web/src/features/chat/components/chat-workspace-shell.tsx apps/web/src/features/chat/components/conversation-list.tsx apps/web/src/features/account/components/profile-menu.tsx apps/web/src/app/page.tsx apps/web/test/chat-mobile-layout.test.tsx apps/web/test/app-shell.test.tsx
git commit -m "feat(web): add chat-first workspace shell and profile menu"
```

### Task 3: Session Continuity for Billing

**Files:**
- Modify: `apps/web/src/app/billing/page.tsx`
- Modify: `apps/web/src/features/auth/auth-session.ts`
- Test: `apps/web/test/billing-page.test.tsx`

**Step 1: Write failing billing hydration test**

```tsx
it("hydrates billing auth from stored session without manual key entry", () => {
  // mock readAuthSession with apiKey
  // render billing page
  // expect account load path uses stored key
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @bd-ai-gateway/web test -- test/billing-page.test.tsx`
Expected: FAIL because billing currently depends on manual input.

**Step 3: Implement billing session hydration**

```tsx
const [apiKey, setApiKey] = useState(() => readAuthSession()?.apiKey ?? "");
```

Auto-load snapshot when hydrated key exists.

**Step 4: Keep advanced key input optional**

Preserve key input as secondary/advanced control, not first-step requirement.

**Step 5: Re-run test to verify pass**

Run: `pnpm --filter @bd-ai-gateway/web test -- test/billing-page.test.tsx`
Expected: PASS.

**Step 6: Commit**

```bash
git add apps/web/src/app/billing/page.tsx apps/web/src/features/auth/auth-session.ts apps/web/test/billing-page.test.tsx
git commit -m "feat(web): hydrate billing from authenticated session"
```

### Task 4: Chat Request State Consistency + Stable Metadata

**Files:**
- Modify: `apps/web/src/features/chat/use-chat-session.ts`
- Modify: `apps/web/src/app/chat/chat-types.ts`
- Modify: `apps/web/src/app/chat/chat-reducer.ts`
- Modify: `apps/web/src/features/chat/components/message-list.tsx`
- Test: `apps/web/test/chat-polish.test.tsx`
- Test: `apps/web/test/chat-reducer.test.ts`

**Step 1: Write failing tests for contradictory state and timestamps**

```tsx
it("does not show success state when chat request fails");
it("preserves message createdAt across rerenders");
```

**Step 2: Run tests to verify fail**

Run: `pnpm --filter @bd-ai-gateway/web test -- test/chat-polish.test.tsx test/chat-reducer.test.ts`
Expected: FAIL with current behavior.

**Step 3: Extend message model with `createdAt`**

```ts
type ChatMessage = { role: "user" | "assistant"; content: string; createdAt: string };
```

Set timestamp on insertion, not render.

**Step 4: Fix request lifecycle handling**

- On non-OK: set error and return early from success path.
- Emit success toast only on successful response.

**Step 5: Re-run tests to verify pass**

Run: `pnpm --filter @bd-ai-gateway/web test -- test/chat-polish.test.tsx test/chat-reducer.test.ts`
Expected: PASS.

**Step 6: Commit**

```bash
git add apps/web/src/features/chat/use-chat-session.ts apps/web/src/app/chat/chat-types.ts apps/web/src/app/chat/chat-reducer.ts apps/web/src/features/chat/components/message-list.tsx apps/web/test/chat-polish.test.tsx apps/web/test/chat-reducer.test.ts
git commit -m "fix(web): stabilize chat request states and message timestamps"
```

### Task 5: Visual System Unification for Auth/Chat/Billing

**Files:**
- Modify: `apps/web/src/app/globals.css`
- Modify: `apps/web/src/app/auth/page.tsx`
- Modify: `apps/web/src/features/billing/components/billing-shell.tsx`
- Modify: `apps/web/src/features/chat/components/message-composer.tsx`
- Test: `apps/web/test/styling-config.test.ts`
- Test: `apps/web/test/auth-page.test.tsx`

**Step 1: Write failing tests for unified class/token usage**

```tsx
it("uses shared dark-first token classes on auth, chat, and billing shells");
```

**Step 2: Run tests to verify fail**

Run: `pnpm --filter @bd-ai-gateway/web test -- test/styling-config.test.ts test/auth-page.test.tsx`
Expected: FAIL with current mixed visual language.

**Step 3: Implement token and shell updates**

- Normalize page shells and card elevations.
- Ensure consistent spacing and heading hierarchy.
- Keep ChatGPT-like structure while preserving BD labels.

**Step 4: Re-run tests to verify pass**

Run: `pnpm --filter @bd-ai-gateway/web test -- test/styling-config.test.ts test/auth-page.test.tsx`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/web/src/app/globals.css apps/web/src/app/auth/page.tsx apps/web/src/features/billing/components/billing-shell.tsx apps/web/src/features/chat/components/message-composer.tsx apps/web/test/styling-config.test.ts apps/web/test/auth-page.test.tsx
git commit -m "feat(web): unify modern chat-first visual language"
```

### Task 6: Regression Verification and Documentation Updates

**Files:**
- Modify: `docs/design/active/2026-02-24-web-flow-critical-review.md`
- Modify: `README.md`
- Modify: `docs/README.md`
- Test: `apps/web/test/chat-auth-gate.test.ts`
- Test: `apps/web/test/billing-page.test.tsx`
- Test: `apps/web/test/chat-polish.test.tsx`

**Step 1: Add/refresh docs for new canonical flow**

- Document `/` guarded chat home.
- Document `/auth` gateway behavior.
- Document avatar menu destinations.

**Step 2: Run targeted regression tests**

Run: `pnpm --filter @bd-ai-gateway/web test -- test/chat-auth-gate.test.ts test/billing-page.test.tsx test/chat-polish.test.tsx`
Expected: PASS.

**Step 3: Run full web test suite**

Run: `pnpm --filter @bd-ai-gateway/web test`
Expected: PASS.

**Step 4: Run web build verification**

Run: `pnpm --filter @bd-ai-gateway/web build`
Expected: PASS and routes compile successfully.

**Step 5: Commit**

```bash
git add docs/design/active/2026-02-24-web-flow-critical-review.md README.md docs/README.md
git commit -m "docs(web): document guarded chat-first flow and UX standards"
```

## Notes for Executor

- Apply @superpowers/test-driven-development discipline per task.
- Keep each commit scoped to one task.
- Request review after each task batch using @superpowers/requesting-code-review.
- Do not change backend billing formulas or API contracts in this web-only track.
