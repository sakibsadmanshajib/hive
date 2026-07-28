# Issue #538 visual proof: payment browser return, both rails

Captured against a running stack after the change, on 2026-07-28.

## Stack under test

- `control-plane` built from this branch, serving `GET /api/v1/accounts/current/checkout/intent`
  and the reduced webhook surface.
- `web-console` from this branch, serving `/console/billing/checkout/return` and
  `/api/payments/return/sslcommerz`.
- Real Supabase project, real `payment_intents` rows, real console session.
- Isolated Docker network and container names, so no shared stack was disturbed.

No payment was processed. Neither rail has credentials configured in this
environment, so the four outcomes were produced by seeding one `payment_intents`
row per terminal state and letting the return page resolve each one through the
account-scoped control-plane read, which is exactly the path a real payer takes
after the provider redirects. A fifth row was seeded on a **different account** to
exercise a crafted return URL.

## Files

| File | What it shows |
| --- | --- |
| `01-balance-before-crafted-return.png` | Billing balance before any return URL was visited. |
| `02-crafted-return-foreign-intent-hint-success.png` | Return URL naming an intent owned by another account, with `hint=success&outcome=success` appended. Renders "We could not find that purchase". No state is leaked and nothing is credited. |
| `03-return-success-sslcommerz.png` | Settled SSLCommerz payment. "Payment complete", credits only. |
| `04-return-pending-sslcommerz.png` | Payment whose webhook has not landed yet. "Confirming your payment". |
| `05-return-failed-with-crafted-success-hint.png` | Failed intent visited with `hint=success` in the URL. Still renders "Payment did not go through". The hint cannot manufacture an outcome. |
| `06-return-cancelled-stripe.png` | Cancelled intent returned from the Stripe rail. |
| `07-balance-after-crafted-returns.png` | Billing balance after all of the above, unchanged. |

## No credit can be created from a return URL

After every screenshot above, including the crafted and replayed ones, the
credit ledger held zero entries for all five intents:

```
ledger entries created for the five proof intents: 0
[]

most recent 0 ledger entries on the proof account:
[]
```

Queried directly against `public.credit_ledger_entries` for
`idempotency_key in (payment:purchase:<each of the five intent ids>)`.
`payment:purchase:<intent id>` is the deterministic key `PostPurchaseGrant` uses,
so its absence means no grant was posted by any of these requests.

## Browser return no longer lands on a webhook

The three SSLCommerz browser return endpoints are gone from the control-plane.
The IPN endpoint, which is the only settlement trigger, still answers:

```
success  HTTP/1.1 404 Not Found
fail     HTTP/1.1 404 Not Found
cancel   HTTP/1.1 404 Not Found
ipn      HTTP/1.1 200 OK
```

SSLCommerz returns the browser with a cross-site form POST, absorbed by the
console and redirected to the return page. A caller-supplied outcome is dropped:

```
$ curl -X POST '/api/payments/return/sslcommerz?intent=<id>&outcome=success' \
    --data 'tran_id=<id>&status=VALID&amount=999.00'
HTTP/1.1 303 See Other
location: http://localhost:13000/console/billing/checkout/return?rail=sslcommerz&intent=<id>
```

Note the response carries neither `outcome` nor `status` nor `amount` forward.
