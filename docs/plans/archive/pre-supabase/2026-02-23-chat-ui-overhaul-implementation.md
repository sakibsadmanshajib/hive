# Chat UI Overhaul Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Deliver a complete shadcn-based, mobile-first frontend overhaul for chat and billing while preserving all existing API behavior.

**Architecture:** Keep route structure (`/`, `/chat`, `/billing`) and backend integration unchanged, but move the UI to Tailwind + shadcn primitives with reusable domain components and extracted chat state logic. Build mobile-first layouts with a responsive sidebar/drawer model, then layer advanced chat rendering and quality-of-life interactions.

**Tech Stack:** Next.js 15 App Router, React 19, TypeScript, Tailwind CSS, shadcn/ui (Radix), lucide-react, react-markdown, remark-gfm, Vitest.

---

### Task 1: Set up Tailwind + shadcn dependencies

**Files:**
- Modify: `apps/web/package.json`
- Create: `apps/web/tailwind.config.ts`
- Create: `apps/web/postcss.config.js`
- Create: `apps/web/components.json`
- Create: `apps/web/src/app/globals.css`
- Modify: `apps/web/src/app/layout.tsx`

**Step 1: Write the failing test (config smoke)**

Create `apps/web/test/styling-config.test.ts`:

```ts
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

describe("styling config", () => {
  it("has tailwind and postcss config files", () => {
    expect(existsSync(resolve(process.cwd(), "tailwind.config.ts"))).toBe(true);
    expect(existsSync(resolve(process.cwd(), "postcss.config.js"))).toBe(true);
  });
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @bd-ai-gateway/web test`
Expected: FAIL because `tailwind.config.ts` and `postcss.config.js` do not exist.

**Step 3: Write minimal implementation**
- Add dependencies: `tailwindcss`, `postcss`, `autoprefixer`, `class-variance-authority`, `clsx`, `tailwind-merge`, `lucide-react`, `react-markdown`, `remark-gfm`, `@radix-ui/*` packages used by initial components.
- Create Tailwind + PostCSS config files.
- Create `globals.css` with base design tokens and dark/light variables.
- Update `layout.tsx` to import `globals.css` and remove inline body styles.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @bd-ai-gateway/web test`
Expected: PASS for `styling-config.test.ts`.

**Step 5: Commit**

```bash
git add apps/web/package.json apps/web/tailwind.config.ts apps/web/postcss.config.js apps/web/components.json apps/web/src/app/globals.css apps/web/src/app/layout.tsx apps/web/test/styling-config.test.ts
git commit -m "feat(web): add tailwind and shadcn foundation"
```

### Task 2: Add shadcn base primitives and utility helpers

**Files:**
- Create: `apps/web/src/lib/utils.ts`
- Create: `apps/web/src/components/ui/button.tsx`
- Create: `apps/web/src/components/ui/card.tsx`
- Create: `apps/web/src/components/ui/input.tsx`
- Create: `apps/web/src/components/ui/textarea.tsx`
- Create: `apps/web/src/components/ui/select.tsx`
- Create: `apps/web/src/components/ui/sheet.tsx`
- Create: `apps/web/src/components/ui/scroll-area.tsx`
- Create: `apps/web/src/components/ui/dropdown-menu.tsx`
- Create: `apps/web/src/components/ui/avatar.tsx`
- Create: `apps/web/src/components/ui/badge.tsx`
- Create: `apps/web/src/components/ui/skeleton.tsx`
- Create: `apps/web/src/components/ui/toaster.tsx`
- Create: `apps/web/src/components/ui/sonner.tsx` (or chosen toast impl)

**Step 1: Write the failing test**

Create `apps/web/test/utils.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { cn } from "../src/lib/utils";

describe("cn", () => {
  it("merges conditional class names", () => {
    expect(cn("a", false && "b", "c")).toBe("a c");
  });
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @bd-ai-gateway/web test apps/web/test/utils.test.ts`
Expected: FAIL with module-not-found for `src/lib/utils`.

**Step 3: Write minimal implementation**
- Add `cn()` helper with `clsx` + `tailwind-merge`.
- Add initial shadcn primitives needed for app shell and chat UI.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @bd-ai-gateway/web test apps/web/test/utils.test.ts`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/web/src/lib/utils.ts apps/web/src/components/ui apps/web/test/utils.test.ts
git commit -m "feat(web): add reusable shadcn primitives"
```

### Task 3: Implement global app shell and responsive navigation

**Files:**
- Modify: `apps/web/src/app/layout.tsx`
- Create: `apps/web/src/components/layout/app-shell.tsx`
- Create: `apps/web/src/components/layout/app-header.tsx`
- Create: `apps/web/src/components/layout/app-sidebar.tsx`
- Create: `apps/web/src/components/layout/theme-toggle.tsx`
- Modify: `apps/web/src/app/page.tsx`

**Step 1: Write the failing test**

Create `apps/web/test/app-shell.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AppShell } from "../src/components/layout/app-shell";

describe("AppShell", () => {
  it("renders chat and billing navigation links", () => {
    render(<AppShell><div>content</div></AppShell>);
    expect(screen.getByRole("link", { name: /chat/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /billing/i })).toBeInTheDocument();
  });
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @bd-ai-gateway/web test apps/web/test/app-shell.test.tsx`
Expected: FAIL because `AppShell` does not exist.

**Step 3: Write minimal implementation**
- Build responsive shell with:
  - Mobile top bar + sheet drawer.
  - Desktop persistent sidebar.
  - Main content slot.
- Move home page into cards/sections that match new shell style.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @bd-ai-gateway/web test apps/web/test/app-shell.test.tsx`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/web/src/app/layout.tsx apps/web/src/app/page.tsx apps/web/src/components/layout apps/web/test/app-shell.test.tsx
git commit -m "feat(web): add responsive app shell and navigation"
```

### Task 4: Extract chat domain state into hook and add reducer tests

**Files:**
- Create: `apps/web/src/features/chat/types.ts`
- Create: `apps/web/src/features/chat/chat-reducer.ts`
- Create: `apps/web/src/features/chat/use-chat-session.ts`
- Create: `apps/web/test/chat-reducer.test.ts`
- Modify: `apps/web/src/app/chat/page.tsx`

**Step 1: Write the failing test**

Create `apps/web/test/chat-reducer.test.ts` with assertions for:
- append user message
- append assistant message
- set loading state
- set auth state

Example test case:

```ts
it("appends assistant response to active conversation", () => {
  const next = chatReducer(initialState, {
    type: "assistant_received",
    payload: { conversationId: "conv_1", content: "hello" },
  });
  expect(next.conversations[0].messages.at(-1)?.content).toBe("hello");
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @bd-ai-gateway/web test apps/web/test/chat-reducer.test.ts`
Expected: FAIL because reducer module does not exist.

**Step 3: Write minimal implementation**
- Add typed reducer and hook.
- Move fetch/auth/send logic out of page into hook.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @bd-ai-gateway/web test apps/web/test/chat-reducer.test.ts`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/web/src/features/chat apps/web/src/app/chat/page.tsx apps/web/test/chat-reducer.test.ts
git commit -m "refactor(web): extract chat state into reducer and hook"
```

### Task 5: Build modern chat UI components (mobile-first)

**Files:**
- Create: `apps/web/src/features/chat/components/chat-shell.tsx`
- Create: `apps/web/src/features/chat/components/conversation-list.tsx`
- Create: `apps/web/src/features/chat/components/message-list.tsx`
- Create: `apps/web/src/features/chat/components/message-bubble.tsx`
- Create: `apps/web/src/features/chat/components/message-composer.tsx`
- Create: `apps/web/src/features/chat/components/typing-indicator.tsx`
- Modify: `apps/web/src/app/chat/page.tsx`

**Step 1: Write the failing test**

Create `apps/web/test/chat-mobile-layout.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import ChatPage from "../src/app/chat/page";

describe("chat page", () => {
  it("renders new chat action and message input", () => {
    render(<ChatPage />);
    expect(screen.getByRole("button", { name: /new chat/i })).toBeInTheDocument();
    expect(screen.getByPlaceholderText(/ask something/i)).toBeInTheDocument();
  });
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @bd-ai-gateway/web test apps/web/test/chat-mobile-layout.test.tsx`
Expected: FAIL until migrated components and semantics are in place.

**Step 3: Write minimal implementation**
- Rebuild `/chat` with:
  - Sidebar + drawer conversation navigation.
  - Sticky composer.
  - Scrollable message pane.
  - Role-based bubble variants and timestamps.
  - Loading + typing states.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @bd-ai-gateway/web test apps/web/test/chat-mobile-layout.test.tsx`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/web/src/app/chat/page.tsx apps/web/src/features/chat/components apps/web/test/chat-mobile-layout.test.tsx
git commit -m "feat(web): implement responsive modern chat interface"
```

### Task 6: Add markdown and code-block rendering with copy action

**Files:**
- Create: `apps/web/src/features/chat/components/markdown-message.tsx`
- Create: `apps/web/src/features/chat/components/code-block.tsx`
- Create: `apps/web/test/markdown-message.test.tsx`
- Modify: `apps/web/src/features/chat/components/message-bubble.tsx`

**Step 1: Write the failing test**

Create `apps/web/test/markdown-message.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MarkdownMessage } from "../src/features/chat/components/markdown-message";

describe("MarkdownMessage", () => {
  it("renders fenced code blocks", () => {
    render(<MarkdownMessage content={"```ts\\nconst x = 1\\n```"} />);
    expect(screen.getByText("const x = 1")).toBeInTheDocument();
  });
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @bd-ai-gateway/web test apps/web/test/markdown-message.test.tsx`
Expected: FAIL because component does not exist.

**Step 3: Write minimal implementation**
- Add markdown renderer using `react-markdown` + `remark-gfm`.
- Add code block wrapper with copy button and accessible labels.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @bd-ai-gateway/web test apps/web/test/markdown-message.test.tsx`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/web/src/features/chat/components/markdown-message.tsx apps/web/src/features/chat/components/code-block.tsx apps/web/src/features/chat/components/message-bubble.tsx apps/web/test/markdown-message.test.tsx
git commit -m "feat(web): render markdown and code blocks in chat"
```

### Task 7: Upgrade billing and usage UI to match new design system

**Files:**
- Modify: `apps/web/src/app/billing/page.tsx`
- Create: `apps/web/src/features/billing/components/billing-shell.tsx`
- Create: `apps/web/src/features/billing/components/usage-cards.tsx`
- Create: `apps/web/src/features/billing/components/topup-panel.tsx`
- Create: `apps/web/test/billing-page.test.tsx`

**Step 1: Write the failing test**

Create `apps/web/test/billing-page.test.tsx` with assertions for:
- API key field visibility
- load account button
- top-up controls

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @bd-ai-gateway/web test apps/web/test/billing-page.test.tsx`
Expected: FAIL until billing components are migrated.

**Step 3: Write minimal implementation**
- Recompose billing page with cards/panels and improved responsive spacing.
- Preserve same fetch calls and business flow.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @bd-ai-gateway/web test apps/web/test/billing-page.test.tsx`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/web/src/app/billing/page.tsx apps/web/src/features/billing/components apps/web/test/billing-page.test.tsx
git commit -m "feat(web): redesign billing and usage dashboard"
```

### Task 8: Add theme provider and persistent light/dark toggle

**Files:**
- Create: `apps/web/src/components/theme/theme-provider.tsx`
- Modify: `apps/web/src/components/layout/theme-toggle.tsx`
- Modify: `apps/web/src/app/layout.tsx`
- Create: `apps/web/test/theme-provider.test.tsx`

**Step 1: Write the failing test**

Create `apps/web/test/theme-provider.test.tsx`:

```tsx
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ThemeProvider } from "../src/components/theme/theme-provider";

describe("ThemeProvider", () => {
  it("applies a theme class to document root", () => {
    render(<ThemeProvider><div>ok</div></ThemeProvider>);
    expect(document.documentElement.className).toMatch(/light|dark/);
  });
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @bd-ai-gateway/web test apps/web/test/theme-provider.test.tsx`
Expected: FAIL because provider does not exist.

**Step 3: Write minimal implementation**
- Add provider with `localStorage` persistence and system fallback.
- Wire theme toggle button in app shell/header.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @bd-ai-gateway/web test apps/web/test/theme-provider.test.tsx`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/web/src/components/theme/theme-provider.tsx apps/web/src/components/layout/theme-toggle.tsx apps/web/src/app/layout.tsx apps/web/test/theme-provider.test.tsx
git commit -m "feat(web): add persistent light-dark theme support"
```

### Task 9: Add quality states, toasts, and keyboard shortcuts

**Files:**
- Modify: `apps/web/src/app/chat/page.tsx`
- Modify: `apps/web/src/features/chat/components/message-composer.tsx`
- Modify: `apps/web/src/features/chat/use-chat-session.ts`
- Create: `apps/web/src/features/chat/hooks/use-chat-shortcuts.ts`
- Create: `apps/web/test/chat-shortcuts.test.ts`

**Step 1: Write the failing test**

Create `apps/web/test/chat-shortcuts.test.ts` for:
- `Enter` sends message
- `Shift+Enter` inserts newline
- shortcut handler ignores empty message

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @bd-ai-gateway/web test apps/web/test/chat-shortcuts.test.ts`
Expected: FAIL until shortcut hook is implemented.

**Step 3: Write minimal implementation**
- Add keyboard shortcut hook.
- Add toast feedback for success/failure actions.
- Add empty/loading/error skeleton states where missing.

**Step 4: Run test to verify it passes**

Run: `pnpm --filter @bd-ai-gateway/web test apps/web/test/chat-shortcuts.test.ts`
Expected: PASS.

**Step 5: Commit**

```bash
git add apps/web/src/features/chat/hooks/use-chat-shortcuts.ts apps/web/src/features/chat/use-chat-session.ts apps/web/src/features/chat/components/message-composer.tsx apps/web/src/app/chat/page.tsx apps/web/test/chat-shortcuts.test.ts
git commit -m "feat(web): add keyboard shortcuts and improved feedback states"
```

### Task 10: Final verification and documentation update

**Files:**
- Modify: `README.md`
- Modify: `docs/README.md`

**Step 1: Write the failing test (doc expectation)**

Add to `apps/web/test/styling-config.test.ts`:

```ts
it("documents upgraded web UI routes", () => {
  // lightweight expectation: doc file contains /chat and /billing mentions
});
```

**Step 2: Run test to verify it fails**

Run: `pnpm --filter @bd-ai-gateway/web test`
Expected: FAIL until documentation updates are included.

**Step 3: Write minimal implementation**
- Update `README.md` web section to reflect modernized chat and billing UI.
- Update `docs/README.md` index with the new design and implementation plan docs.

**Step 4: Run tests/build to verify all pass**

Run:
- `pnpm --filter @bd-ai-gateway/web test`
- `pnpm --filter @bd-ai-gateway/web build`
- `pnpm --filter @bd-ai-gateway/api test`
- `pnpm --filter @bd-ai-gateway/api build`

Expected:
- PASS for web tests.
- Successful web build.
- API test/build remain green (no regressions).

**Step 5: Commit**

```bash
git add README.md docs/README.md
git commit -m "docs(web): document redesigned chat and billing experience"
```
