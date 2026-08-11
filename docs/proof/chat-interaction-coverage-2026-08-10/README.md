# Chat interaction coverage, live, 2026-08-10

Re-measurement after the engine was taught to read HTTP statuses. Run against
https://chat-hive.scubed.co, signed in through the shared live-auth helper,
which mints a session without touching any password.

**248 of 270 enumerated controls proven, 91.9 percent.**

The 2026-08-08 run recorded 196 of 222, 88.3 percent.

## Two controls the old predicate called proven are broken

This engine never registered a response listener, so every status was
invisible to it. Two controls moved from proven to unproven on exactly that:

- settings:General, Admin Settings. Fires a request that answers 404. It was
  counted as network proof.
- composer-more, Upload Files. Renders File not found. and nothing else. It
  was counted as dom proof.

Both look like real defects in the surface rather than gate artefacts.

## Why the ratio rose despite that

The denominator changed shape. search enumerated 106 controls this run against
50 last time and every one of them proved, which alone moves the ratio more
than the two reclassifications move it back. The honest reading is that the
two numbers are not directly comparable: this run swept a different set of
surfaces, and three of them still refuse to open.

## Three surfaces still do not sweep, and they are not in the denominator

#834 merged and deployed, and it was expected to unblock these. It did not:

- sidebar enumerated zero controls.
- chat-item-menu could not open, click timed out after 20s.
- chat-message-actions could not open, same.
- workspace:knowledge enumerated zero controls.

These are surfaceErrors in the ledger and they fail the gate, so they are
visible rather than quietly dropped. They are not exclusions and they carry
no issue, because the gate is supposed to stay red on them until they sweep.

## The one exclusion

composer-controls, in surface-exclusions.json, tied to #844. The gate fails
the moment that issue closes, so the exclusion cannot outlive its reason.

## The denominator guard

surface-floors.json records the control count of every surface this run swept.
A run that enumerates fewer controls on a surface than its floor fails, and so
does a surface with no floor at all. A surface that enumerated zero gets no
floor, because a floor of zero is a bar nothing can fail.

## The break proof

The self test now carries four buttons rather than two, and it is the check
that fails if the detector breaks:

| button | expected | result |
| --- | --- | --- |
| Wired, changes the page | proven | proven |
| Dead, no handler | unproven | unproven |
| Failing, endpoint answers 500 then a toast | unproven | unproven, detail names the 500 |
| Toasty, error toast only | unproven | unproven, raised an error to the user |

Run: npx playwright test --config=e2e/chat-coverage/playwright.chat-coverage.config.ts --grep 'tells a working control'

2 passed in 41.6s against the live deployment, with the fixture served from an
intercepted origin so the failing button has a same origin endpoint whose
status the prover can read.

No credential appears in this directory.

## Run notes: the box timed out twice under our own load

Recorded here because two independent surfaces failing under agent load is a
stability signal rather than noise, and because a silent re-run would have
hidden it.

- The console box returned 504 on seven routes during an earlier attempt at the
  console sweep on the same evening: /console/members, /console/settings/billing,
  /console/settings/profile, /console/setup, /invitations/accept, /no-workspace
  and /oauth/consent. It recovered unattended, matching #815.
- The Supabase magic link mint answered 504 once, which failed a run at the
  authentication step before any control was touched.

Both recovered without intervention. Every figure recorded in this directory
comes from a run with no 504 in it.

## The percentage is not a stable measure yet

The search surface is the chat list, so its control count is the number of
chats the account holds: 52 Chat Menu buttons and 25 New Chat links, one per
row, several of them left behind by other automated runs. The denominator
therefore drifts with data state rather than with the product, which is why
this run is not comparable to the previous one. Recommendation is in PR #809
and is deliberately not implemented here.
