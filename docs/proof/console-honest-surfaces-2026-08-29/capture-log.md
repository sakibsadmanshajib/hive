# Visual proof: three console surfaces made honest

Date: 2026-08-29
Branch: fix/console-honest-surfaces at ba3bc21, which is the head this
capture was taken from, after the two review fixes (credit-space
truncation, and the em dash for an unreadable balance) landed. An earlier
capture of the same four frames was taken before those two commits and is
superseded by this one.
Pull request: 1336
Issues: 1328 (sign-up), 1331 (rotate), 1332 (credit denomination)

## Substrate

Captured against the demo box, with this branch built into its own image and
run beside the live stack, never in place of it.

- Source: a fresh clone of `fix/console-honest-surfaces` at `/tmp/proof1336-src`
  on the box, not the deploy checkout.
- Image: `hive-web-console-prod:proof1336`, built from
  `deploy/docker/Dockerfile.web-console.prod` with the box's own
  `NEXT_PUBLIC_SUPABASE_URL` and `NEXT_PUBLIC_SUPABASE_ANON_KEY`, and with
  `NEXT_PUBLIC_DISABLE_SELF_SERVE_SIGNUP` fed from the box's real
  `ENTERPRISE_DISABLE_SIGNUP`, which is `true`. The gate in these screenshots
  is therefore the deployment's own configuration, not a value picked to make
  the capture look right.
- Container: `proof1336`, attached to the live `hive_default` compose network
  with `CONTROL_PLANE_BASE_URL=http://control-plane:8081`. No host port, no
  change to any running service.
- Data: live. The balance and the API keys below are the real Hive Demo
  workspace rows, read through the live control-plane.
- Session: minted through the admin one-time-token flow in
  `apps/web-console/tests/e2e/support/live-auth.mjs` for
  `demo@hive-demo.invalid`. No password was set, reset or rotated.

## Captured

Playwright 1.62.0 in `mcr.microsoft.com/playwright:v1.62.0-jammy`, viewport
1440x1000 at device scale factor 2, full page. No URL in this run carries a
credential in a query string or a fragment, so nothing needed redaction in the
pixels or in this log.

Console transcript from the capture run, verbatim:

```
01-dashboard-credits | http://proof1336:3000/console | Hive Demo
credits card text: 99,996,364,207 credits, at 1,000,000,000 credits per $1.00
02-api-keys-no-rotate | http://proof1336:3000/console/api-keys | API keys
rotate links on api keys page: 0
03-signup-by-invitation | http://proof1336:3000/auth/sign-up | Accounts are created by invitation
email inputs on the sign-up page: 0
04-signin-no-create-link | http://proof1336:3000/auth/sign-in | Sign in to your console
create-one links on the sign-in page: 0
05-billing-balance | http://proof1336:3000/console/billing | Billing
```

What each frame shows:

1. `01-dashboard-credits.png`, issue 1332. The same balance that read
   `99,996,364,207` with no unit beside it now reads `$99.99`, with
   `Posted $99.99 . Reserved $0.00` under it and the credit figure plus the
   conversion below that. Truncation is visible here rather than asserted: the
   balance is 99.996364207 dollars and the card says 99.99, not 100.00.
2. `02-api-keys-no-rotate.png`, issue 1331. The active key row offers Revoke
   and nothing else, the page counts zero anchors pointing at a rotate href,
   and the header now says how rotation is actually done.
3. `03-signup-by-invitation.png`, issue 1328. The route this deployment
   refuses says so, in the deployment's own words, with no form to submit and
   a link onward to sign-in. Compare the reported failure, which was
   "Something went wrong on our end. Reference AUTH-20260829T012555Z."
4. `04-signin-no-create-link.png`, issue 1328. The sign-in page no longer
   links to that page at all, and says accounts are created by invitation.
5. `05-billing-balance.png`, issue 1332. The second surface that renders the
   same balance, drawn by the same component, so the two pages cannot drift
   apart again. The ledger below it still counts in credits, which is
   deliberate: that is the unit the ledger, the invoices and the spend-alert
   threshold are denominated in.

## Cleanup

`docker rm -f proof1336`, `docker rmi hive-web-console-prod:proof1336`, and
`rm -rf /tmp/proof1336-src /tmp/proof1336-state /tmp/proof1336-shots
/tmp/proof1336-pw` on the box. The storage-state file held a real session and
was removed with the rest; it was never copied off the box and never printed.
