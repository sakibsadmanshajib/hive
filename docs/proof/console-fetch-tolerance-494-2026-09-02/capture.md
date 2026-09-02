# Issue #494 visual proof, 2026-09-02

One failing control-plane fetch used to take down the whole console page. These
captures put the same failure in front of unmodified `main` and in front of this
branch, on a running Next.js production server.

## How the captures were taken

Both stacks are the real `web-console` production server (`next build` then
`next start`) running in the repo's own `deploy/docker` `web-console` service,
each built with `--build` so the image carries that tree's source rather than a
stale one. The control-plane and GoTrue origins are a small fixture server so a
single endpoint can be made to fail on demand without breaking a shared
environment; everything above the HTTP boundary (middleware, Server Components,
the data layer this PR changes, rendering) is the product code.

The only difference between the two stacks is the checkout:

| capture | tree | balance endpoint |
| --- | --- | --- |
| 01 | `origin/main` @ 29876a721 | `503` |
| 02 | `fix/494-console-fetch-tolerance` @ 7120eaadd | `503` |
| 03 | `fix/494-console-fetch-tolerance` @ 7120eaadd | `200` |

`GET /api/v1/accounts/current/credits/balance` is the one endpoint changed
between 02 and 03; the server was not restarted between them, only the fixture's
answer. A spend-alert threshold of 500,000,000,000 credits is configured in every
capture, which is what makes the false-alert bug visible.

URLs carry no credentials, so nothing is redacted. The fixture session is a
fabricated token for a fabricated account (`proof@hive-demo.invalid`,
"Northwind Analytics"); no real user, workspace or balance appears.

## 01 before, `origin/main`, balance 503

`http://localhost:3200/console/billing` answers **HTTP 500**.

Two defects in one frame:

- The page is gone. `Something went wrong on this page` (`app/console/error.tsx`)
  replaces the entire billing surface and the navigation rail, from one 503 on a
  single card's data.
- The banner across the top reads `Your balance has reached or dropped below your
  alert threshold of 500,000,000,000 credits.` That is false. The console has no
  balance at all here; `app/console/layout.tsx` collapsed the rejected read to
  the number 0, and 0 is below every threshold a customer can set. The banner is
  rendered by the layout, so it said this on every console route, not just this
  one.

The boundary's own copy, "The rest of the console is unaffected", is also wrong
on this page.

## 02 after, this branch, balance 503

`http://localhost:3100/console/billing` answers **HTTP 200**.

- The page renders: navigation rail, workspace name, tabs, recent transactions
  (three real ledger rows), spend alerts form, Buy credits.
- The Available balance card reads `Unavailable`, with "We could not read your
  balance just now. Refresh to try again." It does not read 0, and it does not
  read a stale or invented figure.
- No threshold banner. Asserted, not just eyeballed: the page contains zero
  `[role="status"]` elements. The only "alert threshold" text left on the page is
  the `<label>` of the spend-alert input.

## 03 control, this branch, balance 200

Same server, same page, fixture restored to a healthy balance. The card renders
`1,975,350,000,000 credits`, posted 2,000,000,000,000, reserved 12,000,000,000.

This is what makes 02 evidence rather than a coincidence: the card still shows a
real figure when one exists, so the `Unavailable` in 02 is caused by the failing
fetch and not by this change having broken the card.

## Commands

```
# before
cd deploy/docker   # worktree at origin/main
docker compose run --build --rm --no-deps --name proof494main \
  -p 3200:3000 -p 9199:9199 -v <fixture>:/stub.mjs \
  -e BALANCE_STATUS=503 -e STUB_PORT=9199 \
  -e NEXT_PUBLIC_SUPABASE_URL=http://localhost:9199 \
  -e CONTROL_PLANE_BASE_URL=http://localhost:9199 \
  web-console sh -c "node /stub.mjs & npm run build && npm run start -- --hostname 0.0.0.0 --port 3000"

# after
cd deploy/docker   # worktree at fix/494-console-fetch-tolerance
docker compose run --build --rm --no-deps --name proof494 \
  -p 3100:3000 -p 9099:9099 -v <fixture>:/stub.mjs \
  -e BALANCE_STATUS=503 -e STUB_PORT=9099 \
  -e NEXT_PUBLIC_SUPABASE_URL=http://localhost:9099 \
  -e CONTROL_PLANE_BASE_URL=http://localhost:9099 \
  web-console sh -c "node /stub.mjs & npm run build && npm run start -- --hostname 0.0.0.0 --port 3000"

# control: same server, fixture flipped to a healthy balance
docker exec -d -e BALANCE_STATUS=200 -e STUB_PORT=9099 proof494 node /stub.mjs
```

## Console assertions run against capture 02

```
statuses (elements with role="status")            -> []
body contains "Unavailable"                       -> true
body contains "Northwind Analytics"               -> true
body contains ledger rows ("Usage charge")        -> true
only /alert threshold/i match                     -> <label>Alert threshold</label>
```
