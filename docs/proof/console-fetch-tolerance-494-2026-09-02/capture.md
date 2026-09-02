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

```sh
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

# control: same server, fixture flipped to a healthy balance.
# The running fixture holds the port, so a second process cannot simply be
# started beside it (that attempt fails with EADDRINUSE and leaves the 503 in
# place). Stop the first one, then start the replacement detached. The image
# has no ps or pkill, hence the /proc scan.
docker exec proof494 sh -c \
  'for p in /proc/[0-9]*; do grep -qa stub.mjs $p/cmdline 2>/dev/null && kill ${p##*/}; done'
docker exec -d -e BALANCE_STATUS=200 -e STUB_PORT=9099 proof494 node /stub.mjs
```

## Console assertions run against capture 02

```text
statuses (elements with role="status")            -> []
body contains "Unavailable"                       -> true
body contains "Northwind Analytics"               -> true
body contains ledger rows ("Usage charge")        -> true
only /alert threshold/i match                     -> <label>Alert threshold</label>
```

---

# Review round two, same day

Captures 04 and 05 cover the blocker raised in review: billing/alerts collapsed
a failed `listSpendAlerts` to `[]`, which both claimed "No spend alerts
configured yet" about an account that may have several and emptied
`existingThresholds`, the form's only duplicate check.

Same harness as above, same running server for both frames. The fixture's
spend-alert endpoint answers 503 for 04 and 200 for 05; nothing else changes
and the server is not restarted between them.

| capture | tree | spend-alerts endpoint |
| --- | --- | --- |
| 04 | `fix/494-console-fetch-tolerance` @ f2ae65432 | `503` |
| 05 | `fix/494-console-fetch-tolerance` @ f2ae65432 | `200` |

## 04 alerts unreadable

The page renders. "Active alerts" reads "We could not read your spend alerts
just now. Refresh to try again." rather than "No spend alerts configured yet",
and the create form is withheld: its duplicate check is the existing threshold
list, so offering it against a list nobody believes would let a customer create
a second alert at a threshold they already have, under a UI that says it
prevents that.

Asserted rather than eyeballed:

```text
body contains "could not read your spend alerts"  -> true
body contains "No spend alerts configured yet"    -> false
create form present in the DOM                    -> false
page intact (heading + workspace name)            -> true
```

## 05 alerts readable, control

Same server, fixture restored. "1 alert active", the 80% row renders with its
notify address, and the create form is back. This is what makes 04 evidence
rather than a coincidence: the form returns the moment the list can be read,
so its absence in 04 is caused by the failed read and not by this change having
broken the form.

---

# Review round three, same day

Captures 06 and 07 cover the blocking item review called the worst of the four:
`checkout/return` told a payer "We could not find that purchase" when the
payment service was merely unreachable. Someone reading that page has just
paid.

Same harness and same running server for both frames. Only the fixture's
checkout-intent status changes between them.

| capture | checkout-intent endpoint | expected meaning |
| --- | --- | --- |
| 06 | `503` | the status could not be read |
| 07 | `404` | this purchase is genuinely not theirs, or does not exist |

## 06 payment status unreadable

`We could not check your purchase`, with "This is a problem reading the payment
status, not a statement about your payment. If it went through, the credits
appear on the billing page once it is confirmed."

```text
body contains "could not check your purchase"     -> true
body contains "could not find that purchase"      -> false
body contains "not a statement about your payment" -> true
```

## 07 genuinely not found, control

Same server, fixture answering 404. The page returns to `We could not find that
purchase`.

```text
body contains "could not find that purchase"  -> true
body contains "could not check your purchase" -> false
```

This pair is what makes 06 a real distinction rather than a renamed error
state: the two causes now render different pages, and the 404 wording is
unchanged. 403 deliberately renders identically to 404, so the page still
cannot be used to probe for intent ids belonging to other accounts.

---

# Review round four, same day

Captures 08 to 13 cover the blocking item from round three: the budget page
told a member "We could not reach the budget service" about a service that was
answering correctly, and withheld the read-only form that
`tests/e2e/console-budgets.spec.ts` pins. Round four also fixes the same
collapse at two more sites found by sweeping the reads this PR converted
against `authz.Policy`.

The frames come in pairs, one surface at a time. Each pair puts a refusal and
an outage in front of the same page on the same running server, because the
whole claim is that those two are no longer the same render.

| capture | viewer role | endpoint answer | surface |
| --- | --- | --- | --- |
| 08 | member | `403` | `/console/billing/budget` |
| 09 | member | `403` | `/console/billing` |
| 10 | member | `403` | `/console/api-keys/{id}/limits` |
| 11 | owner | `503` | `/console/billing/budget` |
| 12 | owner | `503` | `/console/billing` |
| 13 | owner | `503` | `/console/api-keys/{id}/limits` |

Same harness as the rounds above: the repo's own `web-console` service built
with `--build`, running `next build` then `next start`, with a fixture server
standing in for the GoTrue and control-plane origins. The fixture gained four
knobs for this round, `VIEWER_ROLE`, `WS_BUDGET_STATUS`, `THRESHOLD_STATUS`
and `LIMITS_STATUS`, so a refusal can be served separately from an outage on
each read. The server is not rebuilt between 08 to 10 and 11 to 13; only the
fixture is restarted, by the same `/proc` scan documented in round one.

URLs carry no credentials, so nothing is redacted. The fixture session is a
fabricated token for a fabricated account (`proof@hive-demo.invalid`,
"Northwind Analytics"); no real user, workspace or balance appears.

## 08 member, budget caps refused

This is the frame the blocker is about. The form renders, disabled, with the
read-only notice, which is exactly what a member is supposed to see and what
the spec asserts. Before this round the page rendered an EmptyState instead
and `#budget-soft-cap` did not exist, which is why `console-budgets.spec.ts:77`
failed three times out of three.

```text
#budget-soft-cap present                              -> true
#budget-soft-cap disabled                             -> true
body contains "Only the workspace owner can edit budget caps."  -> true
body contains "We could not reach the budget service" -> false
body contains "Could not load your budget"            -> false
browser console messages                              -> none
```

## 09 member, alert threshold refused

Found by the sweep. `getBudgetThreshold` is read through `billing.view`, which
`authz.Policy` grants to owners only, so every member on the billing overview
read the outage line.

```text
body contains "You cannot view the alert threshold"   -> true
body contains "We could not reach the budget service" -> false
```

Fixing the page was not enough on its own. `getBudgetThreshold` threw a bare
`Error`, so the 403 never reached the caller with its status attached and the
page's refusal branch could not fire. It now throws `ControlPlaneError` like
the rest of the module. The message it carries is unchanged:
`throwControlPlaneError` builds it from the same body field and the same
fallback string that `readResponseError` used.

## 10 member, key rate limits refused

Also found by the sweep. `getApiKeyLimits` is read through `api_keys.read`,
also owner-only, and that page is deliberately written to serve members
read-only, so a refusal is its ordinary answer rather than an edge case.

```text
body contains "You cannot view these rate limits"  -> true
body contains "We could not reach the key service" -> false
```

## 11, 12 and 13 controls, the same three surfaces during a real outage

Same server, fixture answering 503, viewer restored to owner. All three
surfaces return to the unreadable wording, and the budget form is withheld
again.

```text
11: "Could not load your budget"            -> true, #budget-soft-cap present -> false
12: "Could not load your alert threshold"   -> true
13: "Could not load these rate limits"      -> true
```

This is what makes 08 to 10 evidence rather than a renamed error state. The
two causes now render different pages, the outage wording is unchanged, and
the read-only form appears only when the refusal is what actually happened.
