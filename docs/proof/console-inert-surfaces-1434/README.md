# Visual proof: PR #1434, console inert surfaces

Captured 2026-08-29 against a console running this branch on the demo box, signed in as a
run-scoped fixture account. The images live on the permanent `visual-proof-assets` release and are
posted inline on PR #1434 by `scripts/post-pr-visual-proof.sh`; only these text logs are committed,
because `npm run lint:proof-tokens` scans this directory and nothing else.

## Harness

The deployed console (`hive-web-console-prod-1`) serves a production build with no bind mount, so a
branch cannot be proved on it without a rebuild. Instead a second console was stood up on the box
from this branch's source, on the same `hive_default` network and with the same environment as the
live one, published only on `127.0.0.1:3012` and reached over an SSH tunnel:

- source: this branch, rsynced to `/home/sakib/proof-1434/repo`
- container: `proof1434-console`, image `hive-web-console:ci`, whose default command is the same
  `npm run dev` the box itself ran until earlier the same day
- session: minted through the sanctioned admin one-time-token flow in
  `apps/web-console/tests/e2e/support/live-auth.mjs`. No password was set, read or rotated, and the
  shared demo account was not used. The identity is a run-scoped fixture account created by
  `E2E_RUN_KEY=inert1434-…`, so nothing here touches shared fixture rows or real customer data.
- the API key shown is `proof-1434-key`, created through the UI on that fixture account for this
  capture

**`GRAFANA_BASE_URL` was deliberately set** to `http://127.0.0.1:3001` for both runs. That is the
point of the pair: the "before" run shows what setting the variable actually produces, and the
"after" run shows that this branch ignores it. Proving the fix with the variable unset would have
proved nothing, since the tiles were already blank in that state.

No URL captured here carries a credential in its query string, so nothing required redaction. The
account address and the API key id are a throwaway fixture identity, not a credential and not a
customer's.

## before-capture-log.txt: the pre-change component, same environment

`origin/main`'s `observability-tiles.tsx`, with `GRAFANA_BASE_URL` set. Three links render in the
Observability section, two of them live Grafana links:

    link[1] href=http://127.0.0.1:3001/d/hive-platform-overview
    link[2] href=http://127.0.0.1:3001/d/hive-rate-limit

The viewer is an ordinary account owner with no platform-admin or workspace-admin role, and
`/console/analytics` carries no role gate. This is the state that completing the reported wiring
would have shipped: every tenant member handed a link to a Grafana that runs with anonymous Viewer
access, whose rate-limit dashboard queries `topk(10, sum by (key_id, tier) (...))` and so names API
key identifiers across tenants.

`before-01-observability-section.png` is the section, `before-02-analytics-page.png` the page.

## after-capture-log.txt: this branch, same environment

Identical harness and identical `GRAFANA_BASE_URL`. One link renders:

    links inside Observability: 1
    link[0] text="Request logs …" href=/console/logs
    contains a /d/hive- grafana path: false
    contains 'Not available on this deployment.': false

So the Grafana links are gone by construction rather than by an unset variable, and the dead
"not available" state a customer used to see is gone with them.

`after-01-observability-section.png` is the section, `after-02-analytics-page.png` the page.

## The rate limit editor, which this PR does not change

The parity report that prompted this work claimed `/console/api-keys/[id]/limits` was linked from
nowhere. That claim is refuted: it was already fixed on `main` by PR #1394 (issue #543), before this
branch. Both logs capture the live behaviour:

    rate-limit links on the key list: 1
    limits link[0] text="Limits" aria-label="Rate limits for proof-1434-key"
                   href=/console/api-keys/<id>/limits
    [3] clicking the rate-limit link
      landed: …/limits
      h1: "Rate limits"
      nav elements present (console shell): 1
      back link present: true

`after-03-api-keys-list-with-limits-link.png` shows the list entry, and
`after-04-rate-limit-editor.png` the editor it reaches, inside the console shell with its back link.
No code in this PR touches either.
