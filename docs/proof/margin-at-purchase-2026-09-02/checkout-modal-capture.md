# Visual proof: the checkout modal at the new price (PR #1715)

Captured 2026-09-02 on branch feat/1692-margin-at-purchase, at commit 349ece2.

This change moves `price_per_block_minor` from 100 to 106, which is a figure the
checkout modal renders to a customer, so the repository's visual proof rule
applies. Images are posted on the pull request through
scripts/post-pr-visual-proof.sh; this is the text half the proof-token linter
scans.

## What was actually running

Console: built and served from THIS branch's source, not from an image.
`npm ci` in apps/web-console (588 packages) followed by `next dev` on port 3117
against the working tree. Nothing was pulled from a registry image and nothing
cached from an earlier build could have answered, which is the failure mode
PR #1685 had to redo its proof for.

Modal: the real `CheckoutModal` component from
apps/web-console/components/billing/checkout-modal.tsx, mounted by a scratch
page at /proof-checkout that does nothing but render it with an account country
code. The page was deleted after the capture; the component was not modified in
any way for it.

Payload: the exact JSON this branch's `payments.Service.GetCheckoutOptions`
produces, emitted by a scratch Go test that built the real Service and, for the
BD case, the real `FXService` with the admin override standing in for the XE
call, so the effective rate is `fx.go` computing mid x (1 + FXFeeRate) rather
than a number typed into a fixture. Playwright served those bytes to the modal's
own fetch of /api/v1/accounts/current/checkout/rails.

What that does NOT prove, said plainly: no control-plane process was running, so
this is the real server FUNCTION feeding the real component, not an end-to-end
HTTP path through a deployed stack. Standing up control-plane needs Supabase and
S3 credentials that this capture deliberately did not use. The end-to-end half
is covered separately by the database proof in proof.md, where the same
arithmetic was written to and read back from Postgres.

Auth: /proof-checkout is outside the /console prefix the middleware gates, so no
session was needed. A local responder answered 401 to every Supabase auth call
and a placeholder anon key was used. No credential appears in any URL in this
log or in any screenshot, so there is nothing to redact; the only query
parameter in the captured URLs is `country`.

## The payloads the modal was fed

US account:

    {
      "rails": [ { "rail": "stripe", "label": "Card", "currency": "USD",
                   "enabled": true, "min_credits": 1000000000,
                   "max_credits": 100000000000 } ],
      "predefined_tiers": [1000000000, 2000000000, 5000000000,
                           10000000000, 20000000000],
      "price_per_block_minor": 106,
      "credit_block_size": 1000000000,
      "currency": "USD",
      "credit_increment": 10000000,
      "min_credits": 1000000000,
      "max_credits": 100000000000
    }

BD account, same shape, with:

      "price_per_block_minor": 13798,
      "currency": "BDT",

13798 paisa is 106 cents converted at 130.175, which is a mid rate of 127.00
carrying the 2.5 percent FX markup folded into it. Both numbers come from the
service, not from this log.

## The four captures

    01-usd-1-block.png
      url:     http://localhost:3117/proof-checkout?country=US
      typed:   1000000000 credits
      renders: Total  $1.06        (was $1.00 before this change)

    02-usd-20-blocks.png
      url:     http://localhost:3117/proof-checkout?country=US
      typed:   20000000000 credits
      renders: Total  $21.20       (was $20.00)

    03-usd-part-block.png
      url:     http://localhost:3117/proof-checkout?country=US
      typed:   1500000000 credits
      renders: Total  $1.59        (was $1.50)

    04-bdt-no-figure.png
      url:     http://localhost:3117/proof-checkout?country=BD
      typed:   1000000000 credits
      renders: Final amount  Shown on the bKash payment page.

Three amounts rather than one, so the arithmetic is visible rather than a single
number a reader has to trust: 1.06, 21.20 and 1.59 are 1.06 times 1, 20 and 1.5
blocks respectively, and no other multiplier reproduces all three.

Full modal text as read out of the DOM, for 01-usd-1-block:

    Buy credits
    Payment method
    Card
    Credits to purchase
    credits
    Total
    $1.06
    Keep balance
    Continue to payment

and for 04-bdt-no-figure:

    Buy credits
    Payment method
    bKash
    SSLCommerz
    Credits to purchase
    credits
    Final amount
    Shown on the bKash payment page.
    Keep balance
    Continue to payment

## Two things the capture shows that are worth naming rather than cropping

**A BD customer sees no local figure in this modal at all.** The component
branches on the account country and renders "Shown on the bKash payment page"
instead of an amount, which is legacy FX zero-leak behaviour. So the 2.5 percent
folded into the rate is invisible here not because it is hidden deliberately by
this change but because the whole BDT amount is. Capture 04 is that state,
included precisely because the reviewer asked for the BD figure and the honest
answer is that the modal does not show one. The figure a BD payer does see is
rendered by bKash or SSLCommerz on their own page, which needs live rail
credentials to reach and was not captured.

**The under-quote gap I documented is not visible here.** LOW 2 on this pull
request records that the console can quote up to eleven paisa under the charge
on the largest tier, because `floor(106 x 130.175)` drops 0.55 paisa per block.
That gap is in the BDT price, and the modal renders no BDT price, so no capture
here can show it. It is not cropped out: it is on a surface that does not exist
yet, and it is why the fix belongs with the purchase invoice model in #1697
rather than here. The USD price has no such gap, because 106 is a whole number
of cents and the block arithmetic is exact.

## One unrelated defect noticed while capturing, not fixed here

The credits field renders `id="credit-amount"` on both the wrapping div and the
input inside it, so a strict selector on that id resolves to two elements. It is
a duplicate id in the DOM, it predates this change, and correcting it in a
pricing pull request would mix two things. Worth a separate issue.
