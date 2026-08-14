# Interaction coverage gate

Rendering is not functioning. Every test in this repository that asserted a
button *rendered* would have passed against a sidebar button that carried
`aria-haspopup="menu"` and no click handler at all (issue #785), and against a
toggle that flips in the UI and persists nothing. This gate exists to make
that class of defect impossible to ship quietly.

## What it does

1. **Enumerates from the rendered DOM.** For every route, it walks the live
   page and collects everything a user can act on: links, buttons, inputs,
   selects, textareas, `summary`, anything focusable, anything with an
   interactive ARIA role, anything carrying an inline handler, and any element
   whose computed cursor is `pointer` and which is not already inside another
   control. Routes themselves come from `app/**/page.tsx`, not from a list in
   this directory. Adding a page or a control changes the denominator on the
   next run with no edit here, which is the whole point: a hand written list
   only covers what somebody remembered to add to it.

2. **Proves an effect.** Every enumerated control is activated for real and
   must produce something observable:

   | Control | What counts as proof |
   | --- | --- |
   | toggle, checkbox, switch | flip, **reload**, and read the new value back from the server; the gate then restores the original value and **fails** if the restore did not persist |
   | text field | the edit produces an observable consequence (including enabling the Save button beside it), **or** the field is named and inside a form |
   | select | changing the value fires a request, navigates, changes the render, or the select is named and inside a form |
   | internal link | navigation happens and the destination does not answer 4xx or 5xx |
   | external link | the href is an absolute http(s) URL, so clicking it navigates. Destination reachability is checked once per destination and **reported**, never fatal: a third party's uptime does not belong in a merge gate |
   | destructive control, or a submit whose form the gate filled | the application issues its state-changing request and the gate **stops it in the browser** |
   | button, tab, menu item, anything else | a request, a navigation, a download, a popup, a native dialog, or a change to the rendered output |
   | disabled control | nothing. A disabled control is reported in its own bucket and is never proof |

   A request into an endpoint that answers 404, 405, 410 or 5xx is **not**
   proof. That is the exact shape of the console's api-keys, spend-alerts,
   budget and checkout defects: a perfectly observable request into a route
   nobody mounted.

   **Markup is never proof.** A disabled control used to pass on any `title`
   attribute, and a text field on a `name` attribute alone. Both are gone: a
   permission regression that greys a page out, or a deleted `onChange`, has to
   fail something. What it fails is `route-floors.json`, below.

   **The gate does not write to what it measures.** When the values in a
   request are the gate's own invention, or the control's label says delete,
   revoke or purchase, the request is aborted inside the browser and the
   interception is the proof. This is not a preference: the earlier version
   clicked first and pressed Escape afterwards, and its live runs created API
   keys and sent a workspace invitation on the demo tenant. The one deliberate
   exception is a toggle, whose whole proof is that the flip survives a reload;
   the gate flips it back and fails if it could not.

3. **Follows reveals.** When activating a control changes the render, the gate
   re-enumerates and queues whatever appeared. Dropdown items and tab panels
   are part of the surface, not a blind spot, and the gate records the chain
   of clicks that exposed each one.

4. **Holds a floor.** Coverage is proven over enumerated and both sides are
   read from the rendered DOM, so a route that crashes offers fewer controls,
   the denominator shrinks with the numerator, and the percentage holds up
   during an outage. `route-floors.json` names the control identities each
   route must render **and leave enabled**. Presence and reachability, not a
   count: a count recorded against one account's data is unmeetable by another,
   which is why the previous count based floors could only ever be red in CI or
   too low to detect anything.

5. **Fails loudly.** Any control with no proven effect fails the run. So does
   a route that redirects with no declared reason, a route that renders nothing
   at all, a dynamic route with no reachable instance, a missing or disabled
   required control, and a run that measured nothing whatsoever.

## Running it

```bash
cd apps/web-console
npm ci
npx playwright install --with-deps chromium

INTERACTION_BASE_URL=http://localhost:3000 \
INTERACTION_EMAIL=someone@example.com \
SUPABASE_URL=... SUPABASE_ANON_KEY=... SUPABASE_SERVICE_ROLE_KEY=... \
npm run test:interaction
```

There is no password variable. The session is minted through the admin
one-time-token flow in `tests/e2e/support/live-auth.mjs`, which needs no
password and changes none: see `docs/live-test-auth.md`.

`INTERACTION_BASE_URL` also points at a deployed origin unchanged, which is
how the numbers in the pull request were produced. `INTERACTION_ROUTES` takes a
comma separated substring filter for local iteration; a filtered run writes
`coverage.partial.json` instead of `coverage.json` so it can never be mistaken
for the gate number.

In CI the job **must** rebuild the web-console image first. The compose service
bakes source into the image with no volume mount, so a run without `--build`
exercises whatever was last built and reports a green result for stale code.

## Output

`interaction-coverage/coverage.json` carries per-route and overall counts,
every control with its proof type and detail, and the unproven set. The same
document is attached to the Playwright report, so the number is trackable
between runs, along with the disabled set, the external destinations and their
reachability, and any declaration no control in this run matched.

The headline figure is proven over enumerated **distinct control identities**,
which is what `COVERAGE` prints. Raw instance counts are printed underneath and
labelled not comparable between runs, because they move with how many rows an
account happens to hold rather than with what the product offers.

## The registry

A control that is deliberately inert, or whose effect genuinely cannot be
reached from a live run, goes in `control-registry.json`:

```json
{
  "entries": [
    {
      "route": "/console/api-keys",
      "control": "button|Revoke",
      "kind": "covered_elsewhere",
      "reason": "activating it live would revoke the workspace's only API key",
      "owner": "@handle",
      "spec": "tests/e2e/console-workspace-admin.spec.ts",
      "marker": "revoke"
    }
  ]
}
```

`route` accepts `*` for a control that appears in the shared shell on every
page. `kind` is one of:

- `inert` — it genuinely does nothing, and that is the design.
- `covered_elsewhere` — another spec proves it, named by file and by a marker
  string that file must actually contain.
- `known_broken` — this gate found it broken and an issue tracks the fix. The
  issue is mandatory, so the entry expires when the defect is closed and the
  control returns to the measured set.

The gate fails when:

- the justification is empty, shorter than 25 characters, or spans lines
- the owner is missing
- a `covered_elsewhere` entry names no spec, names a spec that does not exist,
  names no marker, or names a marker that spec never mentions
- a `known_broken` entry names no issue
- an entry carries an unknown field, or is declared twice
- an entry names a route that no longer exists (checked in the unit job)

An entry that no control in a run matched is **reported**, not fatal. Whether a
declared control renders can depend on the account: the analytics chart surface
needs usage data before Recharts draws anything, so a CI tenant with no traffic
legitimately never produces it, and failing there would be the same
environment-coupled red the control counts used to be.

## Exclusions expire

Anything that takes a control or a route out of the measured set carries an
owner and an ending. `skip` and `expectRedirect` in `route-fixtures.json`, and
every registry entry, must declare either an `issue` (a deferral, which expires
the moment that issue closes) or `permanent: true` (a standing decision). The
unit job asks the tracker whether the cited issues are still open, and fails
when a blocker has been fixed and its exclusion outlived it.

## Route fixtures

`route-fixtures.json` says how to reach a route that needs more than a bare
path: a query string, a dynamic segment value, a specific session, or an
expected redirect. It can only describe a route, never add or hide one. An
entry naming a route that no longer exists fails the gate.

## Guarding the gate itself

`gate-integrity.test.ts` runs in the ordinary unit job with no browser. It
checks the registry parses and validates, that route discovery still finds the
real routes, that every floor and every declaration names a route that exists,
that no exclusion has outlived its blocker, and that the enumerator still finds
buttons, links, fields, toggles, role-based controls and duplicate names. It
also covers the verdict predicate directly, because a live sweep can go a whole
run without meeting a failing control and the failure branches would otherwise
rot into unreachable code. A gate whose enumerator silently returns nothing
would report perfect coverage, so the enumerator has its own tests.

## Proving it can fail

A gate nobody has watched fail is worth nothing. `INTERACTION_SABOTAGE` takes a
comma separated list of control labels and neuters them at the event layer,
leaving the markup and every sibling untouched:

```bash
INTERACTION_ROUTES=/console/analytics INTERACTION_SABOTAGE=24h,7d,30d,90d \
  npm run test:interaction
```

The same route with and without it is the only evidence that a proven verdict
means anything.
