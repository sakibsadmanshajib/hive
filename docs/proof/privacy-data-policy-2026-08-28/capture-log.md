# Visual proof: console privacy and data-policy page

Date: 2026-08-28
Branch: feat/privacy-data-policy-page
Route added: /console/privacy

## Substrate note (read before judging this capture)

This repo's shared dev `.env` points `SUPABASE_URL` / `SUPABASE_DB_URL` at
project ref `yimgflllgdsbcibnaxqe`. That Supabase Cloud project was deleted
during the self-hosted cutover (`.wolf/decisions.md` context around the
`project_self_hosted_supabase_migration` era). Verified live in this session:

```
$ psql "$SUPABASE_DB_URL" -c 'select 1;'
psql: error: connection to server at "aws-1-us-east-1.pooler.supabase.com" ...
FATAL: (ENOTFOUND) tenant/user postgres.yimgflllgdsbcibnaxqe not found
```

The self-hosted Supabase instance that replaced it lives on the owner's
physical demo box, which this sandboxed agent worktree has no network path
to. Because of that, `getViewer()`/`supabase.auth.getUser()` cannot resolve a
real signed-in session anywhere in this console right now, on any branch, not
just this one. This is a pre-existing environment gap, not something this PR
introduced or could fix.

## What was actually captured

The real, real `app/console/privacy/page.tsx` and the real `ConsoleShell`
nav entry both compile and are exercised end-to-end by the automated test
suite (`__tests__/privacy-page.test.tsx`, `tests/unit/console-shell.test.tsx`),
which mocks only the network boundary (`fetch`) the same way every other
console page test in this repo already does, and all pass.

For a true visual (not just jsdom text assertions), a throwaway,
uncommitted harness route (`app/proofharness/page.tsx`, never added to git,
confirmed absent from `git status` both before and after this capture) called
the *same* `ConsoleShell` + `PageHeader` + `Card`/`Badge` components with the
*same* copy verbatim from the real page, passing mock props directly instead
of calling `getViewer()`/`getCatalogModels()`, since it sits outside
`app/console/layout.tsx` and therefore skips that layout's own
`getViewer()` gate. This was rendered by the actual `next dev` server built
from this branch's own Docker image (`hive-web-console:ci`, rebuilt from this
worktree), not a static mockup, so the real compiled Tailwind CSS, the real
`ConsoleShell` sidebar (including the new "Privacy" nav entry), and the real
copy are what the screenshot shows.

Captured URL: `http://localhost:13000/proofharness` (host-side port mapped
from an isolated, worktree-scoped `docker compose` project;
`COMPOSE_PROJECT_NAME=hive-proof-afbcbc8d6c22c2d2d`, all containers/networks/
volumes removed after capture). No credential-bearing query string anywhere
in this flow: the harness route takes no query params and required no
sign-in.

Tool: `npx playwright@1.62.1 screenshot --viewport-size=1440,1600 --full-page`.

## Cleanup performed

- `rm -rf app/proofharness` (never committed; `git status` clean before PR).
- `docker compose ... down --remove-orphans` plus explicit `docker rm -f` /
  `docker network rm` / `docker volume rm` for every `hive-proof-*` resource.
- `docker-compose.override.proof.yml` (worktree-local port remap, never
  committed) deleted.
- `.env` (copied read-only from the shared checkout to run this stack)
  deleted from the worktree.

## What the screenshot shows

Full-page capture of `/console/privacy`: sidebar with the new "Privacy" nav
entry active, page title "Privacy and data policy", and three cards:
"Request and response content" (retention statement, links to
`/console/logs`), "Where a request goes" (the third-party-infrastructure
disclosure, deliberately without naming which vendor, plus a live-shaped
catalog list), and "Provider allow/block" (the honest not-yet-a-real-control
disclosure, links to `/console/api-keys`). No toggle or switch control is
rendered anywhere on the page, matching the automated test's assertion that
none exists.

## Revision note (same session)

The first capture named "OpenRouter" and "Groq" literally in the "Where a
request goes" card. CodeRabbit's committed-diff review (`coderabbit review
--agent --committed --base main`) flagged this as a major finding: it
violates the console-wide provider-blind convention (`PublicCatalogModel`/
`CatalogModel` both omit a provider field on every other page;
`apps/edge-api/internal/errors/provider_blind_test.go` strips provider
strings from every error response). Re-reading the task brief's own wording
("say so accurately rather than implying otherwise" about routing to
"third-party providers", generic, not "name OpenRouter and Groq") confirmed
the generic phrasing satisfies the honesty requirement just as well without
introducing a new provider-identity leak this product has deliberately never
had anywhere else. The page copy, its file-header comment, and the test
assertions were all corrected to disclose the *fact* of third-party routing
without naming *which* vendor. This screenshot (`privacy-page-v2.png`)
reflects the corrected copy; the original (`privacy-page-full.png`,
superseded, still linked from the PR's review history for context) does
not.

## Revision 3: recapture after the review-round corrections (2026-08-28, later)

Commit under test: the review-round fix that rewrites the four content
claims flagged on PR #1277, plus the adversarial-review follow-ups.

What changed on the page since revision 2, and therefore what this capture
has to show: the metering sentence is now behavioural rather than
structural, a bulleted list names batch jobs, file uploads and RAG
documents as the three surfaces that do store content, the provider
blindness sentence is narrowed to error responses and this page, the
data-collection paragraph is new, the alias sentence no longer claims a
permanent one-to-one mapping, and a fourth card lists what the page does
not cover.

Same substrate constraint as revision 1 and unchanged: the shared dev
Supabase project is deleted, so no real signed-in console session is
obtainable in this sandbox on any branch. The same throwaway, uncommitted
harness route was used, generated mechanically from the real
app/console/privacy/page.tsx by splitting it at its return statement and
substituting literal mock props for the getViewer, getAccountProfile and
getCatalogModels calls. The copy, the components, the compiled Tailwind
CSS and the ConsoleShell sidebar in the screenshot are the real ones from
this branch.

Captured URL: http://localhost:3000/proofharness

No credential appears in that URL, in the page, or in the screenshot. The
harness takes no query parameters and required no sign-in, and the only
identifiers visible in the sidebar are the mock values qa@example.test and
"QA Workspace", which are fixtures, not a real account. The dev server ran
with placeholder Supabase values (https://placeholder.supabase.invalid and
a literal placeholder string) rather than any real key, since the
middleware only needs the client to construct.

Verified in the captured frame: the "Privacy" nav entry is present and
active in the Workspace group, all four cards render, and the two bold
list labels are correctly spaced (an earlier frame in this session showed
"File uploads.Anything" run together, which was fixed in the source before
this capture rather than left in the screenshot).

Tool: npx playwright@1.62.1 screenshot --viewport-size=1440,1800 --full-page

Cleanup performed: the harness route and the generator scripts were
deleted from the worktree (git status clean apart from the two files this
PR touches), and the dev container was stopped and removed.
