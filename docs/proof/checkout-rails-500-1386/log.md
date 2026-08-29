# Buy credits, before and after (issue #1386, PR #1393)

Captured 2026-08-29 with Playwright against a signed-in console. Signed in with
an existing account's existing password through the ordinary sign-in form.
Nothing was set, reset or rotated, per `docs/live-test-auth.md`.

Account under test: `qa-tester@hive.test`, account id
`3104dbfc-2c29-4098-828b-6fa07b52c254`. That is **not** the reserved handover
key `948a1110-a918-4e68-bbb3-dac67dafa56e`. This account has no
`public.account_profiles` row, which is the state issue #999 counted on 14 live
accounts and the state that produced the 500.

No URL in this flow carries a token, code or other credential in its query
string, so there is nothing to redact in either the text below or the screenshot
pixels. The capture script redacts passwords, JWTs and `token`/`code` query
parameters anyway, as a backstop.

## What each frame is

**`01-before-deployed-buy-credits-dead.png`** is the deployed console at
`https://console-hive.scubed.co`, running `main`, as a customer sees it today.

**`02-after-branch-checkout-modal.png`** is this branch's console. It was built
from the working tree by
`docker compose run --build --no-deps web-console-prod` and served on
`http://localhost:3111`, with `CONTROL_PLANE_BASE_URL` pointed at a shim that
proxies every route to the deployed control-plane except
`/api/v1/accounts/current/checkout/rails`, which returns the exact bytes this
branch's fixed handler produced in
`TestGetRailsEndpoint_AccountWithNoProfileRowAnswers200`.

Stated plainly rather than glossed: the deployed control-plane still runs `main`
and still answers 500 on that route, because this branch is not merged and the
box deploys only on a merge to `main`. So the "after" frame is the fixed
server's own payload replayed into a real build of this branch's console, not
the fixed binary serving it. The shim sits **behind** the console's server-side
client, so `getCheckoutRails()` and the
`/api/v1/accounts/current/[...path]` proxy route both run for real against that
payload. That decoder is the half that was dropping every rail item, so it is
the half that most needed exercising rather than mocking.

A capture against the deployed control-plane itself is possible only after this
merges and `deploy-demo-box.yml` runs.

## Capture transcript

```
BEFORE  deployed console (origin main), account qa-tester@hive.test
BEFORE  GET /api/v1/accounts/current/checkout/rails -> 500 {"error":"Upstream service error"}
  clicking the "Buy credits" control at https://console-hive.scubed.co/console/billing
  landed on https://console-hive.scubed.co/console/billing?action=buy
BEFORE  result: NO MODAL: the page is unchanged by the click
BEFORE  rails responses observed during the click: 500 {"error":"Upstream service error"}
BEFORE  screenshot 01-before-deployed-buy-credits-dead.png
AFTER   this branch's console on http://localhost:3111, same account qa-tester@hive.test
  clicking the "Buy credits" control at http://localhost:3111/console/billing
  landed on http://localhost:3111/console/billing?action=buy
AFTER   result: CHECKOUT MODAL RENDERED
AFTER   rails response through the console proxy and its decoder: 200 {"rails":[{"rail":"stripe","currency":"USD","label":"Card","enabled":true}],"credit_increment":10000000,"min_credits":10000000,"max_credits":100000000000,"price_per_block_minor":100,"credit_block_size":1000000000,"currency":"USD"}
AFTER   screenshot 02-after-branch-checkout-modal.png
```

## What the frames show

Before: the click navigates to `/console/billing?action=buy` and the page comes
back identical. No modal, no error, no payment method, nothing. The endpoint
behind it answers `500 {"error":"checkout temporarily unavailable"}` at the
control-plane, which the console proxy reports as
`500 {"error":"Upstream service error"}`.

After: the same click on the same account opens the checkout modal, with
"Payment method: Card" selected, "Credits to purchase" prefilled at the 10,000,000
credit minimum, and a "Total $0.01" priced from `price_per_block_minor = 100`
against `credit_block_size = 1,000,000,000`. "Continue to payment" is enabled.

Note the rails response in the transcript: every rail item carries `rail`,
`currency`, `label` and `enabled`, which is what `decodeCheckoutRail` requires
and what the server never used to send. Before this change that array arrived at
the modal empty even when the request succeeded.

## Reproducing the before frame

```
node apps/web-console/tests/e2e/support/live-auth.mjs <account> https://console-hive.scubed.co/console/billing <state.json>
```

then open `/console/billing` and click "Buy credits", or call the endpoint
directly with that session's bearer:

```
curl -H "Authorization: Bearer <session>" \
  https://control-hive.scubed.co/api/v1/accounts/current/checkout/rails
```
