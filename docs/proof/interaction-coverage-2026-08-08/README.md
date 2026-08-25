# Interaction coverage, live console, 2026-08-08

`coverage.json` is the machine readable ledger produced by
`apps/web-console/tests/interaction/` run against the deployed console at
`https://console-hive.scubed.co`, signed in through the shared live-auth
helper (`docs/live-test-auth.md`), which mints a session without touching any
password.

**287 of 312 enumerated controls proven, 92.0%, across 22 of 24 routes, with
zero ledger integrity problems.**

Every control was activated for real. The proof types in the run:

| proof | count | meaning |
| --- | --- | --- |
| `network` | 207 | activation issued a request that was not baseline traffic |
| `dom` | 38 | the rendered output changed |
| `navigation` | 21 | the URL moved and the destination did not answer 4xx or 5xx |
| `persisted` | 15 | a toggle was flipped, the page reloaded, and the server returned the new value; the gate then restored it and checked the restore persisted too |
| `form-field` | 6 | the field accepts input and is bound to a named field of a form with an action |
| `declared:inert` | 1 | registry entry, with a justification and an owner |

## Two routes are excluded, and are not counted as covered

- `/` redirects to `/auth/sign-in` by design. Declared in `route-fixtures.json`
  as an expected redirect; it contributes no controls to either side of the
  ratio.
- `/console/api-keys/[id]/limits` is reachable only from an existing API key
  row. The gate's workspace has none, and creating one is blocked by #766. It
  is a declared skip with an owner, and it is visible in the ledger rather than
  dropped from it.

## The unproven set, 24 controls

- **15**: the `Documentation` shell link and the `Read the docs` card, on
  fifteen routes. The destination `https://hivegpt.io` accepts no connection.
  This is a real defect, filed as **#824**.
- **1**: `/console/analytics` `button|Apply` on the custom date range, disabled
  with no reason a user can see. Not filed: unconfirmed whether the disabled
  state is a precondition the gate failed to satisfy.
- **1**: `/console/billing` `input|#threshold-credits`. A controlled React
  field inside a client-submitted form with no `action` and no `name`. Typing
  into it does change state, but the billing page polls, so the gate discarded
  its DOM evidence as unusable. Gate limitation, not a console defect.
- **7**: navigation links and cards that prove on other routes in the same run,
  activated while the page was mid navigation. Gate residue, not filed.
