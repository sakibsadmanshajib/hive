# Chat interaction coverage, live, 2026-08-10

Re-measurement after the engine was taught to read HTTP statuses. Run against
https://chat-hive.scubed.co, signed in through the shared live-auth helper,
which mints a session without touching any password.

**144 of 165 distinct control identities proven, 87.3 percent.**

Secondary and not comparable between runs: 248 of 270 raw instances. The
instance figure is the one this file first carried and it is superseded,
because an instance count moves with how many chat rows the account holds
rather than with the product.

## Where the identity figure comes from, exactly

The run itself was taken before the identity metric existed, so its ledger
recorded only the instance counts. It was not re-run to produce the headline:
the demo box is single tenant and another agent is on it. The identity numbers
above and the `summary` block in `coverage.run.json` were recomputed from that
run's own per-control results, offline, by the gate's own `summarise()`:

```
cd apps/web-console
npx vite-node scripts/summarise-chat-coverage-ledger.ts -- \
  ../../docs/proof/chat-interaction-coverage-2026-08-10/coverage.run.json
144/165 identities (87.3%), 248/270 instances (91.8%), 0 not fired
```

A summary is a pure function of the results in the same file, so this is a
derivation and not a new measurement. The function that produces it now has
unit cover (`apps/web-console/tests/unit/chat-coverage-lib.test.ts`), which
runs in the required "Web console" CI job.

The `0 not fired` above is real and expected for this run: the not-fired
category did not exist when it was taken. Every destructive control in it was
clicked with its write aborted at the network layer and recorded as proven,
which is exactly the practice this pull request removes. A run taken after
that change will show a non-zero not-fired count and a correspondingly lower
proven count, and that is a more honest number rather than a regression.

An identity counts as proven only when every instance of it proved, so a
control that works on the first chat row and fails on the seventh is
unproven. The gate fails on the failing instance regardless, because it
fails on any unproven result at all, so collapsing instances cannot hide a
broken one. Each identity carries its instance count in the ledger under
`summary.identityInstances`.

The 2026-08-08 run recorded 196 of 222 instances, 88.3 percent. It predates the
identity metric and is not comparable to either figure above.

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
surfaces, and several of them still refuse to open.

## Five surface errors in this run, none of them in the denominator

#834 merged and deployed, and it was expected to unblock the first four. It
did not. All five are `surfaceErrors` in the ledger and they fail the gate, so
they are visible rather than quietly dropped. They are not exclusions and they
carry no issue, because the gate is supposed to stay red on them until they
sweep:

- sidebar enumerated zero controls.
- chat-item-menu could not open, click timed out after 20s.
- chat-message-actions could not open, same.
- workspace:knowledge enumerated zero controls.
- user-menu, click pass aborted: the execution context was destroyed by a
  navigation partway through the pass, so that surface's numbers in this
  ledger (5 results) are short of what it enumerated (6).

## The one exclusion

composer-controls, in surface-exclusions.json, tied to #844. The gate fails
the moment that issue closes, so the exclusion cannot outlive its reason.

## The denominator guard

surface-floors.json records the highest control count any recorded live run
has enumerated on each surface. A run that enumerates fewer controls on a
surface than its floor fails, a surface with no floor at all fails, and a
floor key the run never swept fails too. Floors are changed only by
`scripts/update-chat-coverage-floors.mjs`, in a separate commit, against a
recorded ledger. The run that checks a floor can never be the run that moves
it.

## The break proof

The self test carries five buttons, and it is the check that fails if the
detector breaks. It needs no deployment and no credentials, so it runs in CI
on every pull request that touches the gate:

| button | expected | result |
| --- | --- | --- |
| Wired, changes the page | proven | proven |
| Dead, no handler | unproven | unproven |
| Failing, endpoint answers 500 then a toast | unproven | unproven, detail names the 500 |
| Toasty, error toast only | unproven | unproven, raised an error to the user |
| Pollster, dead button on a page that polls underneath it | unproven | unproven, the poll was excluded by the idle sample |

```
cd apps/web-console
npm run e2e:chat-coverage:self-check
3 passed (12.1s)
```

Those three are the prover break-proof, the inert-registry and exclusion
validity check, and the exclusion-expiry check that reads issue state from
GitHub. The last one fails without a token rather than skipping, which is why
the CI job passes `GITHUB_TOKEN`. Run locally without one, it fails with
"GITHUB_TOKEN (or GH_TOKEN) is required", which is the intended behaviour.

The fixture is served from an intercepted origin, not from the live
deployment, so the failing button has a same origin endpoint whose status the
prover can read. Nothing in this test talks to the demo box.

No credential appears in this directory. The suite records no trace and no
video by default: both carry the session's cookies, and a trace of the sign-in
hop carries the OAuth callback's `code` and `state`, none of which the text
lint in `tools/lint-no-token-in-proof-captures.mjs` can see inside a binary.
Every URL the suite writes to a message, a ledger or a log goes through
`redactUrl` first.

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

The search surface is the chat list, so its instance count is the number of
chats the account holds: 52 Chat Menu buttons and 25 New Chat links, one per
row, several of them left behind by other automated runs. That is what the
identity figure is for, and it is why the instance figure is kept as the
secondary number rather than dropped: they answer different questions and only
the identity one is comparable between runs.
