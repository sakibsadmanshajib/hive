# Interaction coverage, live console, 2026-08-10

> **This run predates the write guard, and it wrote to the live demo tenant.**
> Reviewed on 2026-08-11: the ledger below records eight form submissions that
> the gate itself filled in and sent for real, including `POST /api/v1/accounts/
> current/api-keys` (an API key named after the probe value) and the workspace
> invitation on `/console/members`, plus a sign-up attempt and a password
> recovery request for `interaction-gate@example.invalid`. No delete or revoke
> ever fired: the only control matching the destructive pattern in either run
> was the `Top up credits` link, and the workspace held no rows carrying a
> revoke control. The gate no longer works this way. It now aborts, inside the
> browser, every state changing request whose values it supplied and anything
> named delete, revoke or purchase, and the interception is what proves the
> control is wired. Read the numbers below as a measurement of the console, not
> as a description of how the gate behaves today.

Re-measurement after two defects were fixed in the gate itself. Run against
the deployed console at `https://console-hive.scubed.co`, signed in through
the shared live-auth helper (`docs/live-test-auth.md`), which mints a session
without touching any password.

**290 of 309 distinct control identities proven, 93.9%, across 22 of 24 routes.**

Secondary and not comparable between runs: 294 of 313 raw instances, 93.9%.
The instance figure is what this file first carried and it is superseded. An
instance count moves with how many rows a page happens to render, so two runs
against different data are not comparable; an identity is one thing a user can
do and does not move with row counts.

An identity counts as proven only when every instance of it proved, so a
control that works in one row and fails in another is unproven. The gate fails
on the failing instance regardless, because it asserts the unproven list is
empty, so collapsing instances cannot hide a broken one.

The 2026-08-08 run recorded 287 of 312 instances, 92.0%. It predates the identity metric and is not comparable to either figure above.

## What changed in the predicate

A control used to be proven by any observable consequence. A click that
answered 500 and rendered an error toast produced both a request and a change
to the render, so the two strongest evidence channels both voted to prove a
control that plainly did not work. The predicate now refuses that:

- A response of 404, 405, 410, 501 or any 5xx is `broken-endpoint`, never proof.
- A 429 is `rate-limited`: the activation produced no usable verdict, so it is
  reported rather than counted either way.
- Any other 4xx is `failed-request`, with one exception below.
- A change to the render whose only new content is an error surface is
  `error-surface`, never proof.

### Expected versus unexpected 4xx, as this run was scored

At the time of this run, a 400, 409 or 422 was excused when the gate had typed
the values the request carried, through `ProofContext.harnessSuppliedInput`.
That exception no longer exists, and neither does the field: the gate now
aborts, inside the browser, every state changing request whose values it
supplied, so no such request reaches a server to be refused. Any 4xx that does
come back is the application's own, and `verdictFromObservation` classifies it
as `failed-request`. Kept here because it is what produced the numbers below.

### What the change actually reclassified: nothing

Across 313 controls the new failure branches fired zero times. No
`broken-endpoint`, no `failed-request`, no `rate-limited`, no `error-surface`
appears in this ledger. The failure mode the review predicted does not occur
on this surface today. That is a finding, not a reason to trust the branches
less: a branch that never fires is a branch nobody has watched work, so
`gate-integrity.test.ts` now drives each of them directly, including the case
where a link navigates successfully while its destination page fetches
something that 500s, which must stay proven and must not be blamed on the link.

The number rose rather than fell for a reason unrelated to the predicate: the
skip of `/console/api-keys/[id]/limits` expired when #766 closed on 2026-08-09,
so it was deleted, and the route is now a loud integrity problem rather than a
silent exclusion.

| proof | count |
| --- | --- |
| `network` | 213 |
| `dom` | 38 |
| `navigation` | 22 |
| `persisted` | 15 |
| `form-field` | 6 |
| `declared:inert` | 1 |
| unproven | 18 |

## The denominator can no longer shrink quietly

At the time of this run, `route-floors.json` recorded the control count of
every route visited. That metric is gone: a count taken against this tenant's
data is not comparable to a CI account with an empty workspace, so it could
only be red forever there or lowered until it detected nothing. The file now
names the control identities each route must render and leave enabled, which
the shell and the pages produce regardless of data. What has not changed is the
rule this section was written for, and it is stronger now: a floor moves only
through a deliberate commit, and there is no longer any command that lets a run
rewrite its own bar. A floor rather than a comparison
against the previous run because a CI job starts from a clean checkout with no
artifact from the last one, so previous-run state can always be missing and
would degrade to no check at all.

## Exclusions now expire

An exclusion has to be either permanent or blocked on a named issue, and the
gate fails once that issue closes. Two exclusions remain and neither hides a
control:

- `/` redirects to `/auth/sign-in` by design and contributes to neither side of
  the ratio.
- The Recharts chart surface on `/console/analytics`, a permanent registry
  entry with no issue.

`/console/api-keys/[id]/limits` was unexcluded for this run: #766 closed, the
skip expired, and the gate reported that it cannot reach the route rather than
pretending the route is not there. It is excluded again as of 2026-08-11, now
citing #885, because reaching it needs an API key to exist and the gate refuses
to create one. That exclusion expires the same way this one did.

## The break proof still holds

Changing what counts as proof means re-running the thing that judges
everything else. Same route, same live origin, same session, twice. The second
run adds `INTERACTION_SABOTAGE=24h,7d,30d,90d`, which blocks those four
controls at the event layer and leaves the markup and every sibling untouched.

| control | clean | handlers blocked |
| --- | --- | --- |
| `button\|24h` | proven, network | **unproven** |
| `button\|7d` | proven, network | **unproven** |
| `button\|30d` | proven, network | **unproven** |
| `button\|90d` | proven, network | **unproven** |
| `button\|Custom` | proven, dom | proven, dom |

Ledgers for both runs are `break-proof-clean.json` and
`break-proof-sabotaged.json` in this directory.

## Reproduce

```
INTERACTION_BASE_URL=https://console-hive.scubed.co \
INTERACTION_EMAIL=<demo account> \
npx playwright test --project=interaction
```

No credential appears in this directory. The ledger records URLs with no query
string carrying a token and no fragment.
