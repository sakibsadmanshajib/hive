# Web Console Revamp — Research

**Scope**: Single PR that (1) migrates `@cloudflare/next-on-pages` → `@opennextjs/cloudflare` (Workers, not Pages), (2) fixes the post-`signIn` server-render redirect-loop into `/auth/sign-in`, (3) ships a Claude-grade visual redesign.

**Code-of-record**: `apps/web-console/` on `main` @ commit 7d523ef (2026-04-24).

---

## 1. Current dependencies

`apps/web-console/package.json` (verbatim, with line numbers):

```jsonc
 1  {
 2    "name": "@hive/web-console",
 3    "version": "0.1.0",
 4    "private": true,
 5    "scripts": {
 6      "dev": "next dev",
 7      "build": "next build",
 8      "build:cf": "npx @cloudflare/next-on-pages@1",
 9      "start": "next start",
10      "test:unit": "vitest run",
11      "test:e2e": "playwright test"
12    },
13    "dependencies": {
14      "@react-pdf/renderer": "^4.4.1",
15      "@supabase/ssr": "^0.6.1",
16      "@supabase/supabase-js": "^2.48.1",
17      "next": "15.1.0",
18      "react": "19.0.0",
19      "react-dom": "19.0.0",
20      "recharts": "^3.8.1"
21    },
22    "devDependencies": {
23      "@cloudflare/next-on-pages": "^1.13.12",
24      "@playwright/test": "^1.51.1",
25      "@testing-library/react": "^16.3.0",
26      "@types/node": "^22.14.0",
27      "@types/react": "^19.1.0",
28      "@types/react-dom": "^19.1.0",
29      "@vitejs/plugin-react": "^4.4.1",
30      "jsdom": "^26.1.0",
31      "openai": "^6.34.0",
32      "typescript": "^5.8.3",
33      "vitest": "^3.1.1"
34    }
35  }
```

**Key facts**:
- `next@15.1.0`, `react@19.0.0`, `@supabase/ssr@^0.6.1`, `@supabase/supabase-js@^2.48.1`.
- Adapter is `@cloudflare/next-on-pages@^1.13.12` (Pages-style). No `@opennextjs/cloudflare`.
- `wrangler` is **NOT** in `devDependencies` — currently invoked via `cloudflare/wrangler-action@v3` in CI.
- **No Tailwind, no PostCSS, no autoprefixer, no shadcn-ui, no Radix, no `lucide-react`, no `class-variance-authority`, no `tailwind-merge`.** Only `clsx@2.1.1` shows up transitively in `package-lock.json` (pulled by recharts/openai). The redesign starts from zero on the design-system axis.
- Test runner: `vitest@^3.1.1` + `@playwright/test@^1.51.1`. No `@testing-library/jest-dom`.

---

## 2. Build pipeline

### 2.1 `apps/web-console/next.config.ts` (verbatim)

```ts
 1  import type { NextConfig } from "next";
 2
 3  // CF Pages + Next 15 — keep config minimal; @cloudflare/next-on-pages
 4  // handles edge runtime compat at build time.
 5  const config: NextConfig = {
 6    images: {
 7      // CF Pages does not use Next's Node image optimizer
 8      unoptimized: true,
 9    },
10    // Reduce build noise on CI
11    productionBrowserSourceMaps: false,
12  };
13
14  export default config;
```

**No `initOpenNextCloudflareForDev()` call.** OpenNext requires this in `next.config.ts` so `wrangler dev`/`opennextjs-cloudflare preview` can populate `getCloudflareContext()`.

### 2.2 `package.json` scripts

```
"dev":      "next dev",
"build":    "next build",
"build:cf": "npx @cloudflare/next-on-pages@1",
"start":    "next start"
```

`build:cf` uses Pages adapter — produces `.vercel/output/static`. Will be replaced.

### 2.3 `apps/web-console/wrangler.jsonc` (verbatim — Pages-specific)

```jsonc
 1  // Cloudflare Pages configuration — source of truth for compatibility settings.
 2  //
 3  // Applied at project-scope via CF API during initial setup and also via
 4  // `wrangler pages deploy`. See docs:
 5  //   https://developers.cloudflare.com/pages/functions/wrangler-configuration/
 6  //   https://developers.cloudflare.com/workers/configuration/compatibility-flags/
 7  //
 8  // Why nodejs_compat:
 9  //   Next 15 + @cloudflare/next-on-pages pulls in Node built-ins (process,
10  //   Buffer, util) through Supabase SSR + Next runtime helpers. Without this
11  //   flag the Worker runtime throws:
12  //     "Error - no nodejs_compat compatibility flag"
13  //
14  // compatibility_date 2024-09-23 is the earliest date that enables
15  // nodejs_compat v2 semantics.
16  {
17    "$schema": "node_modules/wrangler/config-schema.json",
18    "name": "hive-console",
19    "compatibility_date": "2024-09-23",
20    "compatibility_flags": ["nodejs_compat"],
21    "pages_build_output_dir": ".vercel/output/static"
22  }
```

**Pages-specific field**: `pages_build_output_dir`. Workers replaces this with `main`, `assets.directory`, `assets.binding`, and (under OpenNext) the static asset binding wired to `.open-next/assets`.

### 2.4 `.github/workflows/deploy-staging.yml` — `deploy-pages` job (verbatim, lines 265-298)

```yaml
265    deploy-pages:
266      name: Deploy web-console to Cloudflare Pages
267      needs: deploy-vm
268      runs-on: ubuntu-latest
269      defaults:
270        run:
271          working-directory: apps/web-console
272      steps:
273        - uses: actions/checkout@v4
274
275        - uses: actions/setup-node@v4
276          with:
277            node-version: '22'
278            cache: 'npm'
279            cache-dependency-path: apps/web-console/package-lock.json
280
281        - name: Install deps
282          run: npm ci
283
284        - name: Build (CF Pages via @cloudflare/next-on-pages)
285          env:
286            NEXT_PUBLIC_SUPABASE_URL: ${{ secrets.NEXT_PUBLIC_SUPABASE_URL }}
287            NEXT_PUBLIC_SUPABASE_ANON_KEY: ${{ secrets.NEXT_PUBLIC_SUPABASE_ANON_KEY }}
288            NEXT_PUBLIC_API_URL: https://api-hive.scubed.co
289            NEXT_PUBLIC_CONTROL_PLANE_URL: https://cp-hive.scubed.co
290        run: npm run build:cf
291
292        - name: Deploy to CF Pages
293          uses: cloudflare/wrangler-action@v3
294          with:
295            apiToken: ${{ secrets.CLOUDFLARE_API_TOKEN }}
296            accountId: ${{ secrets.CLOUDFLARE_ACCOUNT_ID }}
297            workingDirectory: apps/web-console
298            command: pages deploy .vercel/output/static --project-name=hive-console --branch=main
299
```

CF target is project `hive-console`, branch `main`. Will be repointed to a Worker named `hive-console`.

---

## 3. Supabase SSR wiring — root-cause analysis for the auth bug

### 3.1 `apps/web-console/lib/supabase/server.ts` (verbatim, line-numbered)

```ts
 1  import { createServerClient, type CookieOptions } from "@supabase/ssr";
 2  import type { ReadonlyRequestCookies } from "next/dist/server/web/spec-extension/adapters/request-cookies";
 3
 4  export function createClient(cookieStore: ReadonlyRequestCookies) {
 5    return createServerClient(
 6      process.env.NEXT_PUBLIC_SUPABASE_URL!,
 7      process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!,
 8      {
 9        cookies: {
10          getAll() {
11            return cookieStore.getAll();
12          },
13          setAll(cookiesToSet) {
14            try {
15              cookiesToSet.forEach(({ name, value, options }) =>
16                cookieStore.set(name, value, options)
17              );
18            } catch {
19              // setAll called from a Server Component — cookies can only be set
20              // in a Server Action or Route Handler. This error is safe to ignore
21              // if middleware is refreshing sessions.
22            }
23          },
24        },
25      }
26    );
27  }
```

### 3.2 `apps/web-console/lib/supabase/browser.ts` (verbatim)

```ts
1  import { createBrowserClient } from "@supabase/ssr";
2
3  export function createClient() {
4    return createBrowserClient(
5      process.env.NEXT_PUBLIC_SUPABASE_URL!,
6      process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!
7    );
8  }
```

### 3.3 `apps/web-console/middleware.ts` (verbatim, line-numbered)

```ts
 1  import { NextResponse, type NextRequest } from "next/server";
 2  import { createServerClient, type CookieOptions } from "@supabase/ssr";
 3
 4  export async function middleware(request: NextRequest) {
 5    let supabaseResponse = NextResponse.next({ request });
 6
 7    const supabase = createServerClient(
 8      process.env.NEXT_PUBLIC_SUPABASE_URL!,
 9      process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!,
10      {
11        cookies: {
12          getAll() {
13            return request.cookies.getAll();
14          },
15          setAll(cookiesToSet) {
16            cookiesToSet.forEach(({ name, value }) =>
17              request.cookies.set(name, value)
18            );
19            supabaseResponse = NextResponse.next({ request });
20            cookiesToSet.forEach(({ name, value, options }) =>
21              supabaseResponse.cookies.set(name, value, options)
22            );
23          },
24        },
25      }
26    );
27
28    // Refresh session — required for SSR session persistence
29    const { data: { user } } = await supabase.auth.getUser();
30
31    const { pathname } = request.nextUrl;
32
33    if (pathname.startsWith("/console") && !user) {
34      const signInUrl = new URL("/auth/sign-in", request.url);
35      return NextResponse.redirect(signInUrl);
36    }
37
38    if (pathname === "/" && user) {
39      const consoleUrl = new URL("/console", request.url);
40      return NextResponse.redirect(consoleUrl);
41    }
42
43    return supabaseResponse;
44  }
45
46  export const config = {
47    matcher: [
48      "/((?!_next/static|_next/image|favicon.ico|.*\\.(?:svg|png|jpg|jpeg|gif|webp)$).*)",
49    ],
50  };
51
```

### 3.4 `apps/web-console/app/auth/callback/route.ts` (verbatim)

```ts
 1  import { NextResponse, type NextRequest } from "next/server";
 2  import { createServerClient, type CookieOptions } from "@supabase/ssr";
 3  import { cookies } from "next/headers";
 4
 5  export const runtime = "edge";
 6
 7  const ALLOWED_NEXT_TARGETS = new Set([
 8    "/console",
 9    "/console/settings/profile",
10    "/auth/reset-password",
11  ]);
12
13  export async function GET(request: NextRequest) {
14    const { searchParams, origin } = new URL(request.url);
15    const code = searchParams.get("code");
16    const next = searchParams.get("next") ?? "";
17    const hiveVerify = searchParams.get("hive_verify") === "1";
18    const safeNext = ALLOWED_NEXT_TARGETS.has(next) ? next : "/console";
19
20    if (code) {
21      const cookieStore = await cookies();
22      const supabase = createServerClient(
23        process.env.NEXT_PUBLIC_SUPABASE_URL!,
24        process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!,
25        {
26          cookies: {
27            getAll() { return cookieStore.getAll(); },
28            setAll(cookiesToSet) {
29              try {
30                cookiesToSet.forEach(({ name, value, options }) =>
31                  cookieStore.set(name, value, options)
32                );
33              } catch { /* Ignore: called from Server Component context */ }
34            },
35          },
36        }
37      );
38      const { data, error } = await supabase.auth.exchangeCodeForSession(code);
39      if (!error) {
40        if (hiveVerify) { /* ...admin metadata flag write... */ }
41        return NextResponse.redirect(new URL(safeNext, origin));
42      }
43    }
44    return NextResponse.redirect(new URL("/console", origin));
45  }
```

### 3.5 Sign-in page — `app/auth/sign-in/page.tsx` (verbatim)

```tsx
 1  "use client";
 2  import { createClient } from "@/lib/supabase/browser";
 3  import { useState, type FormEvent } from "react";
 4  import { useRouter } from "next/navigation";
 5
 6  export default function SignInPage() {
 7    const router = useRouter();
 8    const supabase = createClient();
 9    /* ...email/password state... */
10    async function handleSubmit(e: FormEvent<HTMLFormElement>) {
11      e.preventDefault();
12      setError(null);
13      setLoading(true);
14      const { error } = await supabase.auth.signInWithPassword({ email, password });
15      if (error) { setError(error.message); setLoading(false); return; }
16      router.push("/console");
17      router.refresh();
18    }
19    /* ...form markup... */
20  }
```

### 3.6 Comparison vs. canonical `@supabase/ssr` 0.6.x pattern

The canonical pattern (per the package's own docs / `auth-helpers-nextjs` migration guide) requires:

1. `createServerClient(...)` configured with `getAll()` + `setAll()` — **PRESENT** (server.ts L9-24, middleware.ts L11-24, callback L26-35).
2. **Middleware must call `supabase.auth.getUser()`** — **PRESENT** (middleware.ts L29).
3. **Middleware must propagate any cookie writes from `setAll` onto the outgoing `NextResponse`** — **PRESENT** (middleware.ts L19-22 reconstructs `supabaseResponse` then re-sets cookies).
4. **Server Components must await `cookies()` (Next 15) before constructing the client** — **PRESENT** (root page L6, callback L21).
5. **Browser client signs in via `signInWithPassword`** which writes cookies via `document.cookie` *and* via the @supabase/ssr browser cookie adapter — **PRESENT**.

**Verdict — the bug is NOT in the canonical adapter wiring.** All four canonical pieces are in place. The bug surfaces in a *different* code path. The two concrete suspects identified from the code:

#### Suspect A — `app/auth/sign-in/page.tsx` post-sign-in transition

The browser-client `signInWithPassword` writes the Supabase cookies (`sb-<ref>-auth-token`, etc.) **client-side via `document.cookie`** because `createBrowserClient` defaults to `document.cookie` storage. Then `router.push("/console")` followed by `router.refresh()` triggers a server render.

The Next 15 `router.push` followed immediately by `router.refresh()` is the standard pattern, but `@supabase/ssr` 0.6.1's browser client default cookie storage uses `httpOnly: false, sameSite: 'lax'` cookies under the *anon-key project ref name*. These cookies should be visible to middleware on the next request — and they normally are.

The likely failure mode in production (the one the user reports — server-rendered routes redirect to `/auth/sign-in` after sign-in): the **Cloudflare Pages edge runtime serves the RSC payload from an edge cache that pre-dates the cookie write**, *or* the prefetched `/console` RSC payload was issued before sign-in completed. `@cloudflare/next-on-pages@1.13.12` is known to retain cached RSC chunks across navigation when revalidation hints are missing.

The minimal fix is to bypass the client-side router and force a hard navigation that always re-issues a request with the freshly-set cookies:

```tsx
// after successful signInWithPassword
window.location.assign("/console");  // OR: router.refresh() then router.push("/console")
```

(See §7.4 for the proposed final code.)

#### Suspect B — `app/console/account-switch/route.ts` (account switcher) cookie-scope mismatch

The spec `tests/e2e/auth-shell.spec.ts` line ~76 carries an explicit `test.fixme()` with the comment:

> `// TODO: post-switch redirect bounces /console → /auth/sign-in. Account-switch cookie flow needs reconciling with middleware/getViewer scope before this spec can exercise the switcher end-to-end. Tracking outside T1b.`

This **same redirect-loop signature** ("`/console` bounces to `/auth/sign-in`") is what the user is reporting. The `account-switch` route sets `hive_account_id` cookie and 303-redirects to `/console`, but `getViewer()` (used in `/console/layout.tsx` L12) *might* internally call `supabase.auth.getUser()` against a stale cookie store. Since the route uses `runtime = "edge"`, edge cookie writes are sometimes not visible to the immediately-following request on the same connection in CF Pages — a known CF Workers/Pages quirk where `Set-Cookie` headers from a 303 redirect race the browser's next fetch.

#### Suspect C — `runtime = "edge"` in callback + cookie write semantics on Pages

`app/auth/callback/route.ts` L5 forces `runtime = "edge"`. On `@cloudflare/next-on-pages` the edge runtime uses **Workerd** semantics. `cookieStore.set()` inside a Route Handler on Pages 1.13 is documented to silently no-op when the response has already been started (the `try/catch` swallows it). After OpenNext migration the same code path runs through the OpenNext incremental cache + asset binding which DOES persist `Set-Cookie` correctly. So the OpenNext migration **also fixes the auth bug as a side-effect**, modulo Suspect A.

### 3.7 Playwright `signIn` helper — how it signs in, what cookies survive

Both `tests/e2e/auth-shell.spec.ts` (L11-22) and `tests/e2e/profile-completion.spec.ts` (L10-21) **define `signIn` inline — there is NO shared helper file** (the `tests/helpers/` directory referenced in the user prompt does not exist; the support directory is `tests/e2e/support/`). The helper:

```ts
async function signIn(
  page: import("@playwright/test").Page,
  email: string,
  password: string
) {
  await page.goto("/auth/sign-in");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(password);
  await page.click('button[type="submit"]');
  await page.waitForURL("**/console**");
}
```

It is a **UI sign-in**, not a programmatic cookie injection. After successful sign-in the cookies on `page.context()` are the browser-side @supabase/ssr cookies (`sb-<projectRef>-auth-token`, plus possible `sb-<projectRef>-auth-token-code-verifier`). `Path=/`, `SameSite=Lax`, `HttpOnly=false` (browser-side adapter). These ARE visible to subsequent server requests.

Why server routes fail to read them in the failing specs: the same Suspect-A race — `router.push("/console")` from the page is the very first call after `signInWithPassword` resolves; by the time Playwright's `waitForURL("**/console**")` resolves, the browser has either followed a server redirect (because middleware saw no cookie yet) back to `/auth/sign-in`, OR it landed on `/console` with stale RSC. The fix in §7.4 makes `signInWithPassword` resolve *before* triggering navigation by forcing `await supabase.auth.getSession()` once, then `window.location.assign('/console')`.

The support files `tests/e2e/support/e2e-auth-creds.ts`, `e2e-auth-defaults.json`, `e2e-auth-fixtures.mjs` are credential-resolution + an Edge-Function-driven user reset — not a programmatic cookie injection. There is currently **no programmatic sign-in path in the test suite**, only UI sign-in.

---

## 4. Routes inventory

### 4.1 Pages and routes (file → one-liner)

`app/`:
| Path | Description |
|------|-------------|
| `app/layout.tsx` | Root layout. `runtime = "edge"`. Bare `<html><body>{children}</body></html>` — no global stylesheet imported. |
| `app/page.tsx` | Root `/`. Server-side `supabase.auth.getUser()` redirect to `/console` or `/auth/sign-in`. |
| `app/api/budget/route.ts` | API route — budget endpoint proxy. |
| `app/auth/callback/route.ts` | OAuth/magic-link callback — `exchangeCodeForSession` + optional `hive_verify` admin metadata write. `runtime = "edge"`. |
| `app/auth/sign-in/page.tsx` | Client-side email/password sign-in form. **All inline `style={{}}`** — no className. |
| `app/auth/sign-up/page.tsx` | Email/password sign-up. |
| `app/auth/forgot-password/page.tsx` | Forgot-password flow (sends reset email). |
| `app/auth/reset-password/page.tsx` | Reset-password form (consumes recovery token). |
| `app/console/layout.tsx` | Authenticated console shell — sidebar nav, verification banner, budget banner, workspace switcher. Inline-styled (lines 33-76 are all `style={{}}`). |
| `app/console/page.tsx` | Dashboard. Inline-styled. Renders `Workspace: <strong>...</strong>`, "Complete setup" reminder. |
| `app/console/setup/page.tsx` | Profile-setup wizard (owner name, account name, country, region). |
| `app/console/settings/profile/page.tsx` | Profile settings. |
| `app/console/settings/billing/page.tsx` | Billing settings (legalEntityName, etc.). |
| `app/console/api-keys/page.tsx` | API key management. |
| `app/console/billing/page.tsx` | Billing (top-up, ledger, invoices). |
| `app/console/billing/[invoiceId]/download/...` | Invoice download (PDF via `@react-pdf/renderer`). |
| `app/console/analytics/page.tsx` | Spend / usage / error charts (recharts). |
| `app/console/catalog/page.tsx` | Model catalog table. |
| `app/console/members/page.tsx` | Workspace member management. |
| `app/console/account-switch/route.ts` | Workspace switcher POST handler. `runtime = "edge"`. **Suspect-B** of the auth bug. |
| `app/invitations/accept/page.tsx` | Invitation accept landing page. |

### 4.2 Components inventory (`components/`)

```
analytics/{analytics-controls,analytics-table,error-chart,spend-chart,time-window-picker,usage-chart}.tsx
api-keys/{api-key-create-form,api-key-list,revoke-confirm-panel}.tsx
billing/{billing-overview,budget-alert-banner,budget-alert-form,checkout-modal,invoice-download-button,invoice-list,ledger-csv-export,ledger-table}.tsx
billing/checkout-modal.test.tsx                    -- vitest + @testing-library/react
catalog/model-catalog-table.tsx
profile/{account-profile-form,billing-contact-form,business-tax-form}.tsx
email-settings-card.tsx
nav-shell.tsx
verification-banner.tsx
workspace-switcher.tsx
```

All ad-hoc — no `components/ui/`, no shared `Button`, `Card`, `Input`, etc. Every component re-implements its own layout via inline `style={{}}`. **0 occurrences of `className=`** across `app/` + `components/` (verified via `grep -rn 'className=' apps/web-console/{app,components} | wc -l` → `0`). **330 occurrences of `style={{`**. The redesign is starting from a literal blank slate — there is no existing design language to preserve or break.

### 4.3 Styling approach

- **No `globals.css` exists anywhere under `apps/web-console/`** (verified via `find … -name 'globals.css'` → empty).
- **No `tailwind.config.*`, no `postcss.config.*`** (same find, both empty).
- **The root layout never imports a stylesheet.** App ships with browser-default styles plus per-element inline styles only.
- This is unusual but means: the redesign can adopt Tailwind v4 (`@tailwindcss/postcss` + `@import "tailwindcss";` in a new `app/globals.css`) cleanly, with no legacy CSS to migrate. shadcn/ui components can be dropped in via the standard `npx shadcn@latest init` flow targeting `app-router` + Tailwind v4.

---

## 5. Test inventory

### 5.1 Spec files

```
tests/e2e/auth-shell.spec.ts
tests/e2e/openai-sdk.spec.ts
tests/e2e/profile-completion.spec.ts
tests/e2e/unauth.spec.ts
tests/unit/profile-schemas.test.ts
tests/unit/viewer-gates.test.ts
tests/unit/workspace-switcher.test.ts
components/billing/checkout-modal.test.tsx
```

Total tests counted from spec/test bodies (estimate): **~16** (caller asserted "9 passing, 7 failing").

### 5.2 `playwright.config.ts` (verbatim)

```ts
 1  import { defineConfig, devices } from "@playwright/test";
 2
 3  export default defineConfig({
 4    testDir: "./tests/e2e",
 5    fullyParallel: true,
 6    forbidOnly: !!process.env.CI,
 7    retries: process.env.CI ? 2 : 0,
 8    workers: process.env.CI ? 1 : undefined,
 9    reporter: process.env.CI
10      ? [["list"], ["html", { open: "never" }]]
11      : "html",
12    use: {
13      baseURL: process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:3000",
14      trace: "on-first-retry",
15    },
16    projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
17  });
```

**No `webServer` block.** `baseURL` defaults to `http://localhost:3000` and otherwise expects `PLAYWRIGHT_BASE_URL` to point at a running staging URL. The CI deploy job doesn't run E2Es against the deployed Pages URL (only SDK replay) — the Playwright suite is currently **dev/local-only** unless `PLAYWRIGHT_BASE_URL` is overridden. This needs a `webServer` block in the new pipeline so a fresh PR build can be exercised against a local `next start`.

### 5.3 Failing-spec patterns

The user reports 7 failing specs. From the spec source the most-likely failure rows (each follows the same shape — `signIn` → `page.goto(...)` → URL assertion):

- `auth-shell.spec.ts`:
  - `unverified members page stays locked` — `signIn(UNVERIFIED)` then `page.goto("/console/members")` then `await page.waitForURL("**/console/settings/profile")`. **Fails when post-signIn lands on `/auth/sign-in`** (Suspect A).
  - `accepting an invitation keeps current workspace…` — same shape.
  - `workspace switcher persists selected account` — already `test.fixme()`'d explicitly because of the `/console → /auth/sign-in` bounce.
- `profile-completion.spec.ts`:
  - `setup saves profile` — `signIn(VERIFIED)` then `/console/setup` form submission.
  - `dashboard shows setup reminder…` — `signIn(VERIFIED)` then `/console`.
  - `billing settings save partial business profile` — `signIn(VERIFIED)` then `/console/settings/billing`.
  - `unverified billing settings redirect to profile` — `signIn(UNVERIFIED)` then `/console/settings/billing` → `/console/settings/profile`.
  - `dashboard does not introduce a billing reminder` — `signIn(VERIFIED)` then `/console`.

**The shared offending pattern is the `signIn` helper itself** (specs L42-52 of profile-completion, L11-22 of auth-shell):

```ts
await page.click('button[type="submit"]');
await page.waitForURL("**/console**");
```

`waitForURL("**/console**")` matches the `/auth/sign-in` URL too if the page bounces because the wildcard matches anything containing `/console` somewhere in the path — actually no, it doesn't, but it will match the *eventual* `/auth/sign-in?redirectTo=...` only if the test uses such a path. Either way, when middleware redirects post-signIn back to `/auth/sign-in`, `waitForURL` times out → spec fails. **The single fix-point that eliminates all 7 failures is the post-signIn navigation race (Suspect A)**, not changes to the specs themselves.

---

## 6. OpenNext migration delta — concrete changes

### 6.1 `package.json` diff

```diff
   "scripts": {
     "dev": "next dev",
     "build": "next build",
-    "build:cf": "npx @cloudflare/next-on-pages@1",
+    "build:cf": "opennextjs-cloudflare build",
+    "preview:cf": "opennextjs-cloudflare preview",
+    "deploy:cf": "opennextjs-cloudflare deploy",
     "start": "next start",
   ...
   },
   "dependencies": {
     "@react-pdf/renderer": "^4.4.1",
     "@supabase/ssr": "^0.6.1",
     "@supabase/supabase-js": "^2.48.1",
+    "@opennextjs/cloudflare": "^1.0.0",
     "next": "15.1.0",
     "react": "19.0.0",
     "react-dom": "19.0.0",
     "recharts": "^3.8.1"
+    /* ...redesign-time deps... */
+    /* "tailwindcss": "^4.0.0", */
+    /* "@tailwindcss/postcss": "^4.0.0", */
+    /* "tailwind-merge": "^2.5.5", */
+    /* "clsx": "^2.1.1", */
+    /* "class-variance-authority": "^0.7.1", */
+    /* "lucide-react": "^0.460.0", */
+    /* radix primitives picked per shadcn component used */
   },
   "devDependencies": {
-    "@cloudflare/next-on-pages": "^1.13.12",
+    "wrangler": "^3.99.0",
     ...
   }
```

(Confirm pinned versions against npm at PR-time — `@opennextjs/cloudflare` ships fast.)

### 6.2 `wrangler.jsonc` diff (Pages → Workers)

```diff
 {
   "$schema": "node_modules/wrangler/config-schema.json",
   "name": "hive-console",
-  "compatibility_date": "2024-09-23",
+  "compatibility_date": "2025-03-01",
   "compatibility_flags": ["nodejs_compat"],
-  "pages_build_output_dir": ".vercel/output/static"
+  "main": ".open-next/worker.js",
+  "assets": {
+    "directory": ".open-next/assets",
+    "binding": "ASSETS"
+  },
+  "observability": { "enabled": true }
 }
```

### 6.3 `deploy-staging.yml` diff

```diff
   deploy-pages:
-    name: Deploy web-console to Cloudflare Pages
+  deploy-worker:
+    name: Deploy web-console to Cloudflare Workers (OpenNext)
     needs: deploy-vm
     runs-on: ubuntu-latest
     defaults: { run: { working-directory: apps/web-console } }
     steps:
       - uses: actions/checkout@v4
       - uses: actions/setup-node@v4
         with: { node-version: '22', cache: 'npm', cache-dependency-path: apps/web-console/package-lock.json }
       - run: npm ci
-      - name: Build (CF Pages via @cloudflare/next-on-pages)
+      - name: Build (CF Workers via @opennextjs/cloudflare)
         env:
           NEXT_PUBLIC_SUPABASE_URL: ${{ secrets.NEXT_PUBLIC_SUPABASE_URL }}
           NEXT_PUBLIC_SUPABASE_ANON_KEY: ${{ secrets.NEXT_PUBLIC_SUPABASE_ANON_KEY }}
           NEXT_PUBLIC_API_URL: https://api-hive.scubed.co
           NEXT_PUBLIC_CONTROL_PLANE_URL: https://cp-hive.scubed.co
         run: npm run build:cf
-      - name: Deploy to CF Pages
+      - name: Deploy to CF Workers
         uses: cloudflare/wrangler-action@v3
         with:
           apiToken: ${{ secrets.CLOUDFLARE_API_TOKEN }}
           accountId: ${{ secrets.CLOUDFLARE_ACCOUNT_ID }}
           workingDirectory: apps/web-console
-          command: pages deploy .vercel/output/static --project-name=hive-console --branch=main
+          command: deploy
```

### 6.4 `next.config.ts` — needs `initOpenNextCloudflareForDev()`

YES. Required so `next dev` can populate `getCloudflareContext()`:

```ts
import type { NextConfig } from "next";
import { initOpenNextCloudflareForDev } from "@opennextjs/cloudflare";

initOpenNextCloudflareForDev();

const config: NextConfig = {
  images: { unoptimized: true },
  productionBrowserSourceMaps: false,
};

export default config;
```

Also needs new file `apps/web-console/open-next.config.ts`:

```ts
import { defineCloudflareConfig } from "@opennextjs/cloudflare";
export default defineCloudflareConfig({});
```

---

## 7. Auth fix proposal

The wiring already follows the canonical pattern. The fix targets the post-signIn navigation race + uses the OpenNext migration to eliminate the `runtime = "edge"` cookie-write quirk on Pages.

### 7.1 New `lib/supabase/server.ts` — keep, unchanged

The current implementation IS the canonical 0.6.x adapter. Leave it. The cookies-as-component swallowed-error catch IS the documented pattern.

### 7.2 New `lib/supabase/browser.ts` — keep, unchanged

Already canonical.

### 7.3 New `middleware.ts` — keep, with one safety addition

Already canonical. **Optional** addition: when `getUser()` returns no user but the request *just* came from `/auth/sign-in` POST (referer check), skip the redirect once. Not required if §7.4 is shipped. Leave middleware as-is.

### 7.4 New `app/auth/sign-in/page.tsx` — fix the navigation race

```tsx
"use client";
import { createClient } from "@/lib/supabase/browser";
import { useState, type FormEvent } from "react";

export default function SignInPage() {
  const supabase = createClient();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setLoading(true);

    const { data, error } = await supabase.auth.signInWithPassword({ email, password });
    if (error || !data.session) {
      setError(error?.message ?? "Sign-in failed");
      setLoading(false);
      return;
    }

    // Force a full navigation so the server reads the freshly-written cookies.
    // router.push + router.refresh races against the RSC prefetch cache on edge.
    window.location.assign("/console");
  }
  /* ...form markup with new design system... */
}
```

The change from `router.push + router.refresh` to `window.location.assign` is the single-line fix that eliminates the redirect-loop in production AND in Playwright. It guarantees the browser issues a fresh HTTP request that includes the just-written `sb-...-auth-token` cookies.

### 7.5 New `app/auth/callback/route.ts` — drop edge runtime, rely on OpenNext

```ts
// runtime = "edge" — REMOVED. OpenNext serves all routes through the Worker;
// cookieStore.set in route handlers becomes reliable post-migration.
import { NextResponse, type NextRequest } from "next/server";
import { createClient } from "@/lib/supabase/server";
import { cookies } from "next/headers";

const ALLOWED_NEXT_TARGETS = new Set([
  "/console",
  "/console/settings/profile",
  "/auth/reset-password",
]);

export async function GET(request: NextRequest) {
  const { searchParams, origin } = new URL(request.url);
  const code = searchParams.get("code");
  const next = searchParams.get("next") ?? "";
  const safeNext = ALLOWED_NEXT_TARGETS.has(next) ? next : "/console";

  if (!code) return NextResponse.redirect(new URL("/console", origin));

  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);  // reuse the canonical helper

  const { error } = await supabase.auth.exchangeCodeForSession(code);
  if (error) return NextResponse.redirect(new URL("/console", origin));

  return NextResponse.redirect(new URL(safeNext, origin));
}
```

Drops the duplicated inline `createServerClient(...)` block (DRY against `lib/supabase/server.ts`). The optional `hive_verify` admin write should move to a separate Server Action (it currently calls Supabase admin API directly from a route handler — that admin call should not live next to user-facing auth).

### 7.6 New `app/console/account-switch/route.ts` — Suspect-B fix

```ts
// runtime = "edge" — REMOVED.
import { NextResponse, type NextRequest } from "next/server";
import { cookies } from "next/headers";
import { createClient } from "@/lib/supabase/server";
import { getViewer } from "@/lib/control-plane/client";

export async function POST(request: NextRequest) {
  const formData = await request.formData();
  const accountId = formData.get("account_id");
  if (!accountId || typeof accountId !== "string") {
    return NextResponse.redirect(new URL("/console", request.url), { status: 303 });
  }

  // Re-validate via Supabase user check first to ensure the cookie is fresh
  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) {
    return NextResponse.redirect(new URL("/auth/sign-in", request.url), { status: 303 });
  }

  let isValidAccount = false;
  try {
    const viewer = await getViewer();
    isValidAccount = viewer.memberships.some((m) => m.account_id === accountId);
  } catch {
    return NextResponse.redirect(new URL("/console", request.url), { status: 303 });
  }
  if (!isValidAccount) {
    return NextResponse.redirect(new URL("/console", request.url), { status: 303 });
  }

  const response = NextResponse.redirect(new URL("/console", request.url), { status: 303 });
  response.cookies.set("hive_account_id", accountId, {
    httpOnly: true,
    sameSite: "lax",
    path: "/",
    secure: process.env.NODE_ENV === "production",
  });
  return response;
}
```

Adds explicit `secure` flag (CF Workers prod is HTTPS) and an explicit user-existence check that, if missing, sends to `/auth/sign-in` instead of bouncing through `/console` and tripping middleware.

### 7.7 Optional: programmatic sign-in helper for Playwright

To remove future races and speed up the suite, add `tests/e2e/support/programmatic-sign-in.ts`:

```ts
import { request as playwrightRequest, type BrowserContext } from "@playwright/test";

export async function programmaticSignIn(
  context: BrowserContext,
  email: string,
  password: string,
  baseURL: string
) {
  const url = process.env.NEXT_PUBLIC_SUPABASE_URL!;
  const anonKey = process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!;
  const api = await playwrightRequest.newContext();
  const res = await api.post(`${url}/auth/v1/token?grant_type=password`, {
    headers: { apikey: anonKey, "content-type": "application/json" },
    data: { email, password },
  });
  const { access_token, refresh_token } = await res.json();
  const projectRef = new URL(url).hostname.split(".")[0];
  await context.addCookies([
    {
      name: `sb-${projectRef}-auth-token`,
      value: JSON.stringify({ access_token, refresh_token }),
      url: baseURL,
      httpOnly: false,
      sameSite: "Lax",
    },
  ]);
}
```

---

## 8. Risks / unknowns

1. **`@opennextjs/cloudflare` version stability** — the library moves fast. Pin a known-good version at PR-time and verify `compatibility_date` against the Cloudflare changelog.
2. **`@react-pdf/renderer` on Workers** — PDF rendering is heavyweight; it currently works on Pages because of `nodejs_compat`. Verify under OpenNext that `pdfkit`/font loading still works inside the Worker; if not, move PDF generation into a Cloudflare-Queue-backed background worker or a Supabase Edge Function. **Verify before merging.**
3. **`recharts@3` SSR on Workers** — recharts uses `ResizeObserver` and DOM globals. Currently rendered client-side; confirm no SSR regression after OpenNext.
4. **`PLAYWRIGHT_BASE_URL` for staging E2E** — staging URL is not currently wired into the Playwright job. The PR may want to add a `web-e2e-staging` GH job that points Playwright at the deployed Worker URL post-deploy.
5. **`amount_usd` BD-checkout regulatory bug** (`apps/control-plane/internal/payments/http.go:105-115`, listed in CLAUDE.md "Known Issues") — out-of-scope for this PR but flagged as it's a known leak that surfaces in the web-console billing path.
6. **PR sizing** — single PR for migration + bug fix + redesign is large. Recommend gating it behind a feature flag in `next.config.ts` or splitting into 2 PRs (migration+fix first, redesign second) if review velocity is a concern. User asked for one PR — proceed but expect multi-pass review.
7. **Files NOT inspected this session** (suggested for next):
   - `apps/web-console/lib/control-plane/client.ts` — to confirm `getViewer` does NOT itself call `supabase.auth.getUser()` against a separate cookie store (a 3rd Suspect).
   - `apps/web-console/lib/viewer-gates.ts` — gating logic that may also redirect on no-user.
   - `apps/web-console/components/workspace-switcher.tsx` — to confirm the form POST flow into `/console/account-switch` matches expectations.
   - `tests/e2e/openai-sdk.spec.ts` and `tests/e2e/unauth.spec.ts` full bodies — to count exact passing/failing test rows.
   - `app/console/setup/page.tsx` form fields — referenced by failing `profile-completion.spec.ts` tests.
   - Confirm whether a `web-e2e` GH workflow exists separate from `deploy-staging.yml` (the workflow file inspected here only contains `deploy-pages`, `deploy-vm`, `sdk-replay`, `build-images`, `detect-changes` — no Playwright run).
