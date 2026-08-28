# Visual proof: D-045 navigation and console credit display

Three findings from a walkthrough of the running demo box on 2026-08-28, two of
them contradictions of D-045, which was already recorded and believed shipped.

1. `/agent-workspace/tasks` still resolved and rendered its own separate "Sign
   in to run agent tasks" page, which is the second sign-in gate D-045 ruling 1
   rejected. Unlinked from the UI, reachable from any old bookmark.
2. The chat sidebar still carried a Knowledge row. Distinct from #1109, which is
   about that row being dead; this is about the row existing at all, which
   D-045 ruling 2 eliminates.
3. The console rendered "Available credits" as an unlabelled nine digit integer
   (observed `458,419,464`). At D-046's unit that is about 46 cents shown as
   though it were 458 million of something.

Two captures, and they are not the same kind of evidence. Read the label on
each before quoting it.

## Capture 1 and 2: chat surface. Live render of built branch code.

`run-chat.sh` plus `capture-chat.mjs`. Outputs `redirect.log`,
`chat-capture.log`, `01-sidebar-no-knowledge.png`,
`02-agent-workspace-redirects-to-chat.png`.

What ran: `hive-open-webui:d045nav`, built from this worktree with
`deploy/docker/Dockerfile.open-webui`, behind a `caddy:2-alpine` mounting this
repository's own `deploy/docker/Caddyfile.owui`. Not a proof-only Caddyfile: the
redirect under test is a line in the shipped one, so a copy would prove nothing.

No Supabase, no control plane and no provider key are involved. Open WebUI runs
with `WEBUI_AUTH=False` and `OFFLINE_MODE=True`, which is what makes this
capture independent of the shared pool that was saturated tonight and of every
other agent's stack on this machine. Nothing outside the two throwaway
containers was touched, and both were removed on exit.

The redirect is recorded twice on purpose, once at the protocol level with curl
and once through a real browser navigation, because a 308 in a header and a
browser actually landing on the composer are different claims.

## Capture 3: console balance card. FIXTURE capture.

`run-console.sh` plus `capture-console.mjs` and `fixture-page.tsx.txt`. Outputs
`console-capture.log` and `03-console-balance-in-usd.png`.

This one is a fixture capture and is labelled as such in the log itself, not
only here. Both real balance surfaces (`/console` and `/console/billing`) sit
behind `getRequestContext` in
`apps/web-console/lib/control-plane/client.ts`, which validates a Supabase
session with a server side `getUser()` round trip. There was no Supabase this
run could legitimately use: the shared pool was saturated by concurrent agents,
and the only self hosted Supabase running on this machine belongs to another
agent's session, which the fleet rules put out of bounds.

So the real `BillingOverview` component from this branch is rendered inside the
real Next.js application, against the real stylesheet, with fixture props, via a
route that exists only while the script runs and is deleted afterwards. What is
fixture is the balance numbers and the absence of a control plane. What is real
is the component, the formatter and the CSS. An unauthenticated route that
renders balances is not committed to the console; its source is kept here as
`fixture-page.tsx.txt` so the capture is reproducible without shipping it.

The dashboard card at `/console` carries the identical change and is covered by
the same unit tests (`apps/web-console/tests/unit/console-credit-display.test.tsx`
asserts both surfaces), but it is not in this screenshot, because it cannot be
rendered without the session described above. Stated rather than implied.

## Numbers in the capture

`458,419,464` credits, the figure observed on the live box, which is
`$0.458419464`. `formatUsdFromCredits` renders it as `$0.458`: three significant
digits, never fewer than two decimals, never a `$0.00` for a real balance. That
last property is the one the money surfaces depend on, and it is pinned by
`apps/web-console/lib/format/model-pricing.test.ts` and again here.

## Credentials

None appear in any log or any screenshot in this directory. No flow captured
here carries a token in a URL, and no account was signed in: the chat capture
runs with authentication disabled and the console capture renders a component
with literal props.
