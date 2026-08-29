# Buy credits: no selectable rail, before and after

Branch: `fix/buy-credits-rails`
Date: 2026-08-29
PR: #1438

## Substrate, stated plainly

Two console images, both built from this repository, run one after the other
against the **live demo box**:

- before: `hive-web-console:railsbefore-1438`, built from commit
  `908e48a9` (this branch's parent, i.e. `main` at the time) via
  `git archive 908e48a9 | docker build -f deploy/docker/Dockerfile.web-console -`.
- after: `hive-web-console:railsproof-1438`, built from this branch's tree.

Both containers ran with `CONTROL_PLANE_BASE_URL` pointed at the demo box's
real control-plane through an SSH forward, and `SUPABASE_URL` /
`NEXT_PUBLIC_SUPABASE_URL` pointed at the box's own GoTrue through a local
Host-rewriting reverse proxy in front of a second SSH forward. So the page is
this repository's code and the data is the deployment's real data. Nothing was
stubbed, and the `checkout/rails` payload below is what the box actually
served, byte for byte, on both runs.

The image tag `hive-web-console:ci` is deliberately NOT used here. It is shared
across every worktree on this machine, and a sibling agent's cached rebuild
replaced it mid-run, which briefly made a correct build appear to still carry
the defect. Private tags per capture.

Session: minted through the sanctioned admin one-time-token flow in
`apps/web-console/tests/e2e/support/live-auth.mjs` with `E2E_RUN_KEY` set. No
password was set, reset or rotated. The reserved API key id was never touched,
and no payment was initiated against any rail.

Account: the existing run-scoped OWNER fixture
(`e2e-verified+…@scubed.com.bd`, workspace "E2E Verified Workspace"), reused
rather than reseeded.

## What the deployment actually serves

Identical on both runs, captured from the browser's own network log:

```
GET /api/v1/accounts/current/checkout/rails -> 200
{"rails":[{"rail":"stripe","currency":"USD","label":"Card","enabled":false}],
 "credit_increment":10000000,"min_credits":10000000,"max_credits":0,
 "price_per_block_minor":100,"credit_block_size":1000000000,"currency":"USD"}
```

One rail, disabled. `max_credits` 0 against `min_credits` 10,000,000.

The cause of the empty rail list is not this repository's code. The box has no
payment rail credentials at all: `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`,
`BKASH_APP_KEY`, `SSLCOMMERZ_STORE_ID`, `HIVE_PAYMENTS_STUB` and `HIVE_ENV`
are all absent from its `.env`, and control-plane says so at boot:

```
2026/08/29 16:14:15 payments: 0 rail(s) active: []
```

No feature gate is involved: the `payments` package contains no reference to
`featuregate` at all, so issue #756 does not reach this path.

## Before, on `908e48a9`

URL: `http://localhost:3000/console/billing`, "Buy credits" clicked.

```
number inputs in the DOM:
  [{"id":"threshold-credits","min":"1","max":"","step":""},
   {"id":"credit-amount","min":"10000000","max":"0","step":"10000000"}]
"Continue to payment" controls rendered: 1
"Keep balance" controls rendered: 1
console errors observed after modal open: 0
```

`credit-amount` carries `min="10000000" max="0"`: the maximum is below the
minimum, which is not a valid HTML5 number range. The Payment method fieldset
renders empty and Continue to payment is present but permanently disabled,
with nothing on screen saying why. Screenshot: `before-02-modal.png`
(posted on the PR).

## After, on this branch

Same URL, same account, same server response.

```
--- modal text ---
Buy credits

No payment method is available for this account yet, so credits cannot be
bought here. Your balance and your existing API keys are unaffected. Contact
support to have a payment method enabled for this account.

Keep balance
--- end modal text ---
number inputs in the DOM:
  [{"id":"threshold-credits","min":"1","max":"","step":""}]
"Continue to payment" controls rendered: 0
"Keep balance" controls rendered: 1
console errors observed after modal open: 0
```

`credit-amount` is gone from the DOM entirely, so there is no inverted range to
render. The remaining `threshold-credits` input belongs to the Spend alerts
card further down the billing page and is unrelated to checkout. The dead
Continue control is gone, the state is explained, and Keep balance still
closes the modal. Screenshot: `after-02-modal.png` (posted on the PR).

## Not proven here

Nothing in this capture exercises a successful purchase, because the
deployment has no rail that could complete one. The purchase path is covered by
unit tests only (`checkout-modal-ui.test.tsx`, `checkout-modal.test.tsx`,
`checkout-rails-range-guard.test.ts`), and a live purchase capture still needs
a deployment with real rail credentials.
