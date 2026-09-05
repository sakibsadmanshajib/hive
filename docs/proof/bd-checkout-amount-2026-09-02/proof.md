# Visual proof: a BD customer now sees the taka amount at checkout (issue #1737)

Captured 2026-09-02 on branch `fix/1737-bd-checkout-amount`, off `origin/main`
at 18369ff2f.

This change puts a money figure on a customer surface, so the repository's
visual proof rule applies. The images are posted on the pull request through
`scripts/post-pr-visual-proof.sh`; this is the text half that
`npm run lint:proof-tokens` scans.

## What was actually running

Console: built and served from THIS branch's source. `npm ci` in
`apps/web-console` (591 packages) followed by `next dev` on port 3137 against
the working tree. Nothing was answered from a prebuilt image and nothing cached
from an earlier build could have served the component, which is the staleness
failure PR #1685 had to redo its proof for.

Modal: the real `CheckoutModal` from
`apps/web-console/components/billing/checkout-modal.tsx`, mounted by a scratch
page at `/proof-checkout` that renders it and nothing else. The page was deleted
after the capture and the component was not modified for it.

Payload: the exact JSON this branch's `payments.Service.GetCheckoutOptions`
produces. It was emitted by a scratch Go test that built the real `Service` with
all three rails registered and the real `FXService`, with the admin override
standing in for the XE call at a mid rate of 127, so the effective rate is
`fx.go` computing mid x (1 + FXFeeRate) rather than a number typed into a
fixture. Playwright served those bytes to the modal's own fetch of
`/api/v1/accounts/current/checkout/rails`.

What this does NOT prove, stated plainly:

- No control-plane process was running. This is the real server function feeding
  the real component, not an end-to-end HTTP path through a deployed stack.
  Standing up control-plane needs Supabase and S3 credentials this capture
  deliberately did not use.
- No payment rail was contacted and nothing was charged. Rails are stubbed
  locally and no bKash, SSLCommerz or Stripe credential exists in this
  environment, so the second half of the issue's verification ask, that the
  displayed figure matches what the rail then charges, was NOT verified against
  a live rail. It was verified against the charge the control plane computes:
  `TestCheckoutOptions_QuotedRailPriceEqualsTheChargedAmount` prices four
  quantities on all three rails through `PriceForCredits`, the function
  `InitiateCheckout` charges with, and compares it against the figure the
  console arithmetic produces from the published fraction.

Auth: `/proof-checkout` sits outside the `/console` prefix the middleware gates,
so no session was needed. Every Supabase auth call was answered 401 by a local
responder and a placeholder anon key was used. No URL in this log or in any
screenshot carries a query string at all, so there is nothing to redact.

## The payloads the modal was fed

BD account, verbatim from the branch's `GetCheckoutOptions`:

    {
      "rails": [
        { "rail": "stripe", "label": "Card", "currency": "USD", "enabled": true,
          "min_credits": 1000000000, "max_credits": 100000000000,
          "price_minor_numerator": 53, "price_credits_denominator": 500000000 },
        { "rail": "bkash", "label": "bKash", "currency": "BDT", "enabled": true,
          "min_credits": 1000000000, "max_credits": 300000000000,
          "price_minor_numerator": 275971,
          "price_credits_denominator": 20000000000 },
        { "rail": "sslcommerz", "label": "SSLCommerz", "currency": "BDT",
          "enabled": true, "min_credits": 1000000000,
          "max_credits": 5000000000000, "price_minor_numerator": 275971,
          "price_credits_denominator": 20000000000 }
      ],
      "predefined_tiers": [1000000000, 2000000000, 5000000000, 10000000000,
                           20000000000],
      "credit_increment": 10000000,
      "min_credits": 1000000000,
      "max_credits": 100000000000
    }

US account, same source:

    {
      "rails": [
        { "rail": "stripe", "label": "Card", "currency": "USD", "enabled": true,
          "min_credits": 1000000000, "max_credits": 100000000000,
          "price_minor_numerator": 53, "price_credits_denominator": 500000000 }
      ],
      "predefined_tiers": [1000000000, 2000000000, 5000000000, 10000000000,
                           20000000000],
      "credit_increment": 10000000,
      "min_credits": 1000000000,
      "max_credits": 100000000000
    }

## Where 275971 / 20000000000 comes from

Mid rate 127, the worked example in D-066. The 2.5 percent markup is folded into
the rate inside `fx.go`, giving an effective 130.175, and it is never itemised
anywhere on the modal. One credit then costs

    1.06 (D-065 purchase markup) x 130.175 / 10,000,000 (credits per US cent)

paisa, which reduces to 275971 / 20000000000 exactly. The console floors
`credits x 275971 / 20000000000` in BigInt, which is the same fraction truncated
the same direction as `PriceForCredits`.

## Captures

| file | account | rail | credits | rendered |
| --- | --- | --- | --- | --- |
| 1737-01-bd-bkash-one-block.png | BD | bKash | 1,000,000,000 | BDT 137.98 |
| 1737-02-bd-bkash-twenty-blocks.png | BD | bKash | 20,000,000,000 | BDT 2,759.71 |
| 1737-03-bd-card-twenty-blocks.png | BD | Card | 20,000,000,000 | $21.20 |
| 1737-04-us-control-one-block.png | US | Card | 1,000,000,000 | $1.06 |
| 1737-05-us-control-twenty-blocks.png | US | Card | 20,000,000,000 | $21.20 |

Rows 4 and 5 are the control: the dollar figures are the ones PR #1715 already
captured, unchanged by this branch.

Row 3 is the second defect this change fixes. A BD account is offered Stripe
alongside the two taka rails, and `InitiateCheckout` charges in the currency the
SELECTED rail settles in, so selecting Card on a BD account has to reprice in
dollars rather than relabel the taka figure. It does.

Row 2 is the paisa the previous wire dropped. The exact charge for twenty blocks
is 275,971 paisa. The predecessor published the block price already truncated to
13,798 paisa and let the console multiply, landing on 275,960 and under-quoting
by 11 paisa. Both figures are pinned in
`TestGetCheckoutOptions_BDAccount_TruncatesViaMathBig`.

## Browser transcript

Console output during the capture, in full. Nothing but Playwright's own HMR
line and React's dev-tools notice; no error, no warning.

    [bd] console info: %cDownload the React DevTools for a better development experience: https://react.dev/link/react-devtools font-weight:bold
    [bd] console log: [HMR] connected
    [bd] url=http://localhost:3137/proof-checkout rendered total="BDT 137.98" -> 1737-01-bd-bkash-one-block.png
    [bd] url=http://localhost:3137/proof-checkout rendered total="BDT 2,759.71" -> 1737-02-bd-bkash-twenty-blocks.png
    [bd] url=http://localhost:3137/proof-checkout rendered total="$21.20" -> 1737-03-bd-card-twenty-blocks.png
    [us] console info: %cDownload the React DevTools for a better development experience: https://react.dev/link/react-devtools font-weight:bold
    [us] console log: [HMR] connected
    [us] url=http://localhost:3137/proof-checkout rendered total="$1.06" -> 1737-04-us-control-one-block.png
    [us] url=http://localhost:3137/proof-checkout rendered total="$21.20" -> 1737-05-us-control-twenty-blocks.png

## Before

Not captured on this branch, because the code that produced it is gone from it.
For a BD account the same box rendered the words "Final amount" and "Shown on
the bKash payment page." with no figure of any kind, which is the screenshot in
issue #1737 and in PR #1715's own capture log.
