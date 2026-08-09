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
   | toggle, checkbox, switch | flip, **reload**, and read the new value back from the server; the gate then restores the original value and checks the restore persisted too |
   | text field | the field accepts the value **and** is bound to a named form field or the edit itself moves the page |
   | select | changing the value fires a request, navigates, changes the render, or submits as a named form field |
   | internal link | navigation happens and the destination does not answer 4xx or 5xx |
   | external link | the destination is reachable |
   | button, tab, menu item, anything else | a request, a navigation, a download, a popup, a native dialog, or a change to the rendered output |
   | disabled control | a reason the user can see (`title`, `aria-describedby`, or a marked hint) |

   A request into an endpoint that answers 404, 405, 410 or 5xx is **not**
   proof. That is the exact shape of the console's api-keys, spend-alerts,
   budget and checkout defects: a perfectly observable request into a route
   nobody mounted.

3. **Follows reveals.** When activating a control changes the render, the gate
   re-enumerates and queues whatever appeared. Dropdown items and tab panels
   are part of the surface, not a blind spot, and the gate records the chain
   of clicks that exposed each one.

4. **Fails loudly.** Any control with no proven effect fails the run. So does
   a route that redirects with no declared reason, a dynamic route with no
   reachable instance, and a registry entry that has rotted.

## Running it

```bash
cd apps/web-console
npm ci
npx playwright install --with-deps chromium

INTERACTION_BASE_URL=http://localhost:3000 \
INTERACTION_EMAIL=... INTERACTION_PASSWORD=... \
npm run test:interaction
```

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
between runs. The headline format is `proven / enumerated`.

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
page. `kind` is `inert` or `covered_elsewhere`. The gate fails when:

- the justification is empty, shorter than 25 characters, or spans lines
- the owner is missing
- a `covered_elsewhere` entry names no spec, names a spec that does not exist,
  names no marker, or names a marker that spec never mentions
- an entry carries an unknown field, or is declared twice
- an entry matches no control any run enumerated (a stale declaration)

Inertness has to be a recorded decision, never an accident, and the record has
to stay true. An empty entry buys nothing.

## Route fixtures

`route-fixtures.json` says how to reach a route that needs more than a bare
path: a query string, a dynamic segment value, a specific session, or an
expected redirect. It can only describe a route, never add or hide one. An
entry naming a route that no longer exists fails the gate.

## Guarding the gate itself

`gate-integrity.test.ts` runs in the ordinary unit job with no browser. It
checks the registry parses and validates, that route discovery still finds the
real routes, and that the enumerator still finds buttons, links, fields,
toggles, role-based controls and duplicate names. A gate whose enumerator
silently returns nothing would report perfect coverage, so the enumerator has
its own tests.
