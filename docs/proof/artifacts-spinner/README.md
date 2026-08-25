# /artifacts dead end, before and after (issue #1110)

The `/artifacts` path on the chat origin matched no route in the chat
frontend. The SPA fallback served the app shell document for it, so the path
never 404ed at the protocol level: a signed-in visitor got the app's boot
spinner with nothing behind it (the parity audit captured exactly that as an
unresolved Loading region, screenshot `22-chat-artifacts-route.png` in the
audit's own proof directory), and a signed-out visitor was silently bounced
into the console OAuth consent chain. Nothing artifact related ever rendered,
and no error state ever appeared. The issue asks for a minimal working index
behind the path, list plus open, or its removal.

## Reproduction substrate

An isolated throwaway compose stack on this machine, project `artspin1110`,
own ports (chat on 13033, edge-api on 18085, control-plane on 18086, Postgres
on 15433), own volumes, own generated credentials. Self-hosted Supabase plane
from `docker-compose.enterprise.yml` (db, auth, rest, storage, init,
caddy-supabase), in-stack Redis, edge-api, control-plane, open-webui, and
caddy-owui. The full Hive migration chain (101 migrations) applied with
`scripts/ci-throwaway-db.sh --gotrue`. A throwaway user signed in through the
chat password form (enabled for the throwaway only; production keeps the form
off), and two chats seeded directly into the chat store, one carrying an HTML
code block, one carrying an SVG code block.

## Before

Signed in, `GET /artifacts`:

- `before-artifacts-route-404.png`: the pre-fix bundle renders the bare
  `404: Not Found` error page with no app chrome and no artifact surface. On
  the audit's older deployed bundle the same path instead showed the app shell
  with an unresolved Loading region (their screenshot, referenced above);
  either way the path is a dead end with no working artifact surface and no
  error message naming the problem.
- Signed out, the same path silently bounces through the console OAuth
  consent chain (observed live on chat-hive during triage; no screenshot, the
  navigation is fast).

## After

Signed in, `GET /artifacts` on the fixed bundle:

- `after-artifacts-index.png`: the route now renders a real artifacts index.
  The two seeded artifact chats appear as rows; opening one shows the
  sandboxed inline preview with a link back to the source chat.
- `after-artifacts-preview.png`: the HTML artifact preview open in the page.
- `after-artifacts-error.png`: the error state, captured by stopping the
  open-webui backend mid-session so the chat fetch fails: the page shows an
  explicit "Could not load your artifacts" message with a Retry button. The
  same state covers the 20 second timeout path, so no failure mode of the
  fetch renders as an eternal spinner.

## Checks

`scripts/test-owui-hive-frontend.sh` passes: 9 test files, 78 tests,
including the new `artifactIndex.test.ts` (4 tests) covering the timeout
helper and the iframe document builder. The same sources run again inside the
image build via `npm run test:frontend -- --run`.
