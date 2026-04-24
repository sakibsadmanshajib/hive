# Next Session: web-console UI framework + styling

## Context

The v1.0 Go rewrite shipped `apps/web-console` with **zero styling infrastructure** — no Tailwind, no shadcn, no CSS files, no `import "./globals.css"` in `app/layout.tsx`. All pages render with browser default UA styles, so staging (`https://console-hive.scubed.co`) looks "broken" but is actually working correctly — just unstyled.

Staging deploy (`chore/single-level-subdomains` → main) completed 2026-04-24 and is verified live:

- `https://api-hive.scubed.co/health` → 200
- `https://cp-hive.scubed.co/health` → 200
- `https://console-hive.scubed.co` → 307 → `/auth/sign-in` (renders, unstyled)
- 4 models live: `hive-auto`, `hive-default`, `hive-embedding-default`, `hive-fast`

SDK test pass rates against staging:
- **JS (vitest)**: 25 pass / 1 fail (pre-existing fixture path bug) / 1 skip
- **Python (pytest)**: 14/14 pass
- **Java (gradle)**: BUILD SUCCESSFUL

## Goal for this session

Add a modern UI framework to `apps/web-console`, style the existing pages (sign-in, sign-up, forgot-password, console dashboard, billing, api-keys, catalog, analytics, profile, invitations), ship via PR.

## Constraints

- **Never push directly to main** — feature branch + PR only
- **Cloudflare Pages + edge runtime compat** — any framework must build inside `@cloudflare/next-on-pages@1` (current: `wrangler.jsonc` w/ `nodejs_compat`, `compatibility_date: 2024-09-23`)
- **Next.js 15.1.0 + React 19.0.0** — don't downgrade
- **No backend changes** — UI only. Zero touching of `apps/edge-api` / `apps/control-plane` / `supabase/migrations`
- **BD regulatory rule** — no FX rate / currency exchange language in any customer-visible UI
- **.env already has** `NEXT_PUBLIC_SUPABASE_URL`, `NEXT_PUBLIC_SUPABASE_ANON_KEY`

## Step-by-step

1. **Check session prompt**: this file is `.planning/next-session-ui-styling.md`
2. **Confirm clean branch** — start from fresh `main`, create `feat/web-console-ui-styling`
3. **Invoke `openwolf reframe`** workflow per `.wolf/OPENWOLF.md` — read `.wolf/reframe-frameworks.md` decision flow, present comparison matrix, pick framework w/ user input
   - Default recommendation: **Tailwind v4 + shadcn/ui** (best CF Pages + Next 15 + edge runtime compat, zero runtime CSS)
   - Alternatives to present: Mantine (heavier), Chakra (RSC friction), MUI (bundle size)
4. **Scaffold** framework per chosen prompt — adapt to actual paths in `.wolf/anatomy.md`
5. **Style pages in order** — sign-in → sign-up → console layout (nav-shell) → dashboard → billing → api-keys → catalog → analytics → profile → invitations
6. **Verify edge-runtime build** — `cd apps/web-console && npm run build:cf` must succeed with no runtime errors
7. **Run designqc** — `openwolf designqc --routes /auth/sign-in /console /console/billing /console/api-keys` to capture screenshots
8. **Review screenshots** — evaluate against shadcn + WCAG + visual hierarchy
9. **Iterate** until design clean
10. **Open PR** via `gh pr create` — include screenshots in PR body

## Known test bugs to fix during this session (trivial, grab as chores)

- `packages/sdk-tests/js/tests/models/list-models.test.ts:14` — local fixture path `../../../../fixtures/golden` is off by one dir; should be `../../../fixtures/golden`
- `packages/sdk-tests/js/tests/embeddings/embeddings.test.ts:7` — fallback chain `HIVE_EMBEDDING_MODEL ?? HIVE_TEST_MODEL ?? "hive-embedding-default"` picks chat model when only `HIVE_TEST_MODEL` is set; should be `HIVE_EMBEDDING_MODEL ?? "hive-embedding-default"` (drop the chat-model fallback)

## Flaky to watch

- `completion_tokens=0` / `output_tokens=0` intermittently from chat/responses (upstream LiteLLM→OpenRouter usage reporting). Tracks as issue — do not block styling work on it.

## Files to read first (in anatomy order)

- `apps/web-console/app/layout.tsx` (~50 tok) — root layout, no CSS import currently
- `apps/web-console/app/auth/sign-in/page.tsx` — raw HTML form to be restyled
- `apps/web-console/components/nav-shell.tsx` — to become main app shell
- `apps/web-console/wrangler.jsonc` — CF Pages compat settings (DO NOT CHANGE without reason)
- `apps/web-console/next.config.ts` — minimal, keep minimal

## PR commit convention

`feat(web-console): add <framework> styling + shadcn components`

Follow `<type>: <description>` — types: feat, fix, refactor, docs, test, chore, perf, ci
