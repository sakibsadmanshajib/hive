# Dark mode semantic token contrast fix: live capture log

Branch: `fix/491-dark-mode-semantic-tokens`
Date: 2026-08-29
Issue: #491

## Substrate, stated plainly

`hive-web-console:ci`, built from this branch's own tree
(`docker compose --profile dev up --no-deps --build web-console`, `next dev`
on port 3000), run standalone against a placeholder `.env`
(`NEXT_PUBLIC_SUPABASE_URL=https://placeholder-proof.supabase.co`,
`NEXT_PUBLIC_SUPABASE_ANON_KEY` a placeholder string, both `NEXT_PUBLIC_*`
and safe to fabricate since neither is a secret and neither is committed).
Not the demo box: the demo box still serves `main`, which does not carry
this fix yet, so a capture against it would show the pre-fix bug rather than
the change. No control-plane, no database, no auth backend: the sign-in
page's client-side error path (`supabase.auth.signInWithPassword` failing
against the placeholder host) is what renders the real, unmodified danger
text, and needs none of that infrastructure to do it.

`page.emulateMedia`/`newPage({ colorScheme: "dark" })` forced Chromium's
`prefers-color-scheme: dark`, exercising the exact media block this fix
touches. Playwright launched directly (`chromium.launch()`), not through the
MCP tool, matching the task's instruction. No SOCKS proxy needed: the target
is `localhost`, not a `*.scubed.co` public hostname.

## What the screenshot shows

`proof-491-dark.png`, one frame, `http://localhost:3000/auth/sign-in`, dark
theme:

1. The real product danger-text path. Fields filled with throwaway values
   (`proof-491@example.com` / `not-a-real-password`, neither a real
   credential), form submitted, `supabase.auth.signInWithPassword` fails
   against the placeholder Supabase host, and the component's own
   `role="alert"` error text renders in `text-[var(--color-danger)]`
   (`app/auth/sign-in/page.tsx`), completely unmodified by the proof script.
   This is the single worst pre-fix figure in the whole issue (danger badge
   text measured 2.65:1; this exact token against canvas measured 3.65:1),
   now legible red text on the dark background.
2. A labelled swatch overlay, injected by the capture script via
   `page.evaluate`, top right. Three rows, each styled with
   `color: var(--color-danger|warning|success)` (the identical CSS custom
   properties `globals.css` defines, not a copy of their values), labelled
   with the token name and, for danger and success, the worst pre-fix ratio
   from the issue. This is the part of the screenshot that is not a
   pre-existing product surface, stated plainly rather than left implicit:
   it exists because no single real page in the app renders all three
   solid-token colours together, and three separate screenshots would prove
   the same thing with more noise. It resolves through the exact same
   cascade the product ships, in the same loaded page, so what is rendered
   is still the real fix, just also labelled for a reader who cannot run a
   contrast meter over the pixels themselves.

No URL in this capture carries a query string at all, so there is nothing
for `npm run lint:proof-tokens` to have needed to catch.

## Numeric result (full table with every touched pairing in the PR body)

Worst before: danger badge text on danger-soft background, 2.65:1. Worst
after: 5.18:1. Every row in the acceptance table (vault plan doc and PR
body) clears its WCAG AA threshold (4.5:1 normal text, 3:1 non-text) after
this fix; none did before it for the danger and success rows.
