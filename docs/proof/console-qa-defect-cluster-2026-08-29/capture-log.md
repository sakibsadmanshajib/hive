# Console QA defect cluster: capture log

Date: 2026-08-29. Branch `fix/console-qa-defect-cluster-1400`.
Issues covered: #1400, #1401, #1403, #1406, #1408 (item 1). Verdict recorded
for #1402. Split out: #1405, #1408 (item 2).

No credential appears in any URL, log line or screenshot in this capture. The
pages below are local `file`-served static renders; nothing was signed in and
no live session was minted.

## Why this is not a live-stack capture

`docker compose --profile local up` still cannot reach a database from this
sandbox. The checked-out `.env` points at the deleted Supabase Cloud project
(issue #1254, closed as recorded rather than fixed), and the self-hosted
replacement is reachable only on the demo box's internal Docker network by
design: `deploy/cloudflare/tunnel-ingress.json` publishes five hostnames and
none of them is Supabase. So no local sign-in, no local end-to-end run.

## Method

Real React rendering, real compiled CSS, real browser. Only the network
boundary is absent: components are rendered directly with fixtures, the same
seam the committed unit tests already mock.

1. `docker compose run --no-deps --build web-console npm run build` produced
   the real compiled Tailwind chunk (`.next/static/chunks/*.css`), copied out
   through a mounted volume.
2. A throwaway vitest file (`tests/unit/__visual_proof_a254f766__.test.tsx`,
   deleted before commit, not in this pull request's diff) called the real
   `ApiKeyList`, `AnalyticsOverviewSection` and `AnalyticsTable` components
   and `react-dom/server`'s `renderToStaticMarkup`, writing HTML out through
   the same volume. `AnalyticsOverviewSection`'s tiles come from the real
   `deriveOverviewTiles`, not from a hand-built tiles object.
3. Each page loads the Geist and Geist Mono web fonts the console's root
   layout loads through `next/font`, so text metrics match the deployed page
   rather than a fallback stack.
4. Pages were served over `127.0.0.1` and opened in a real Chromium instance
   (chrome-devtools MCP). Horizontal overflow was read as
   `window.scrollTo(9999, 0)` then `window.scrollX`, because
   `documentElement.scrollWidth` reports the wrong number on this layout.
5. "Before" pages use the same harness against `origin/main`'s
   `api-key-list.tsx` and `analytics-overview-section.tsx`, swapped in with
   `git show origin/main:<path>` and restored immediately after capture.
   `git diff --stat` confirmed the restoration before any commit.

Charts are absent from these renders on purpose: recharts' `ResponsiveContainer`
measures its parent at runtime and produces nothing under
`renderToStaticMarkup`. The chart axis half of #1403 is covered by
`__tests__/analytics-api-key-group-labels.test.tsx`, which asserts on the props
the real page hands `SpendChart`.

## #1400 — API key nickname

Fixture: two keys, one with a 5000-character nickname (the shape found live on
two accounts), one ordinary. Viewport 1440.

Before (`shot-api-keys-before.png`):

```text
table width          45524
wrapper scrollWidth  45524
Revoke button left   45579   (44,139 px past the right edge of the viewport)
```

After (`shot-api-keys-after.png`):

```text
table width          1166
wrapper scrollWidth  1166
Revoke button left   1222    visible: true
```

The long nickname renders as an ellipsis with the full value on the element's
`title`. This is the half that repairs a row already stored: the server-side
cap added in the same change only stops new ones.

Server side, `MaxNicknameLen` is 100 characters, counted in runes, enforced in
`decodeMintBody` which both the create and the rotate route now share. A past
`expires_at` is refused there too. Covered by
`apps/control-plane/internal/apikeys/mint_validation_test.go`.

## #1401 — CSV formula injection

Not a rendered surface, so no screenshot. The evidence is the bytes the real
component writes to its Blob, asserted in
`components/billing/ledger-csv-export.test.tsx` and
`__tests__/console-cache-visibility.test.tsx`.

Before, the logs export replaced quotes, commas and newlines with spaces and
passed a leading `=` straight through; the ledger export stripped commas from
one column and escaped nothing else. Both now go through `lib/csv.ts`.

```text
before  2026-08-21T09:30:00Z,topup,50000000,idemx-with-commas
after   2026-08-21T09:30:00Z,topup,50000000,"idem,x-with-commas"

before  ...,=HYPERLINK( http://qa.invalid   qa2-csv )
after   ...,"'=HYPERLINK(""http://qa.invalid"",""qa2-csv"")"
```

A numeric column is left unprefixed so the ledger's credits column still sums
in a spreadsheet. That exemption is carried by the cell's type rather than by
sniffing the string, because a text column's `-001` is not a number the reader
wants normalised to `-1` on the next save:

```text
csvCell(-2000)    "-2000"     a number, from a numeric column
csvCell("-001")   "'-001"     text, preserved exactly
csvCell(" =1+1")  "' =1+1"    leading whitespace does not hide the payload
```

The leading-whitespace case is the standard bypass of a check anchored at
index 0, since a spreadsheet that trims a cell on import sees what the raw
first character hid.

Covered here: the request logs export and the billing ledger export, the two
the issue names. Not covered, and reported separately: Open WebUI's own admin
exports (`Settings/Database.svelte` `exportUsers`, `Evaluations/Feedbacks.svelte`
`feedbacksToCsv`) have the same gap on the chat surface.

## #1403 — spend grouped by API key

Viewport 1440. Fixture rows are the three from the issue, including the
unattributed bucket.

Before (`shot-spend-by-key-before.png`): the GROUP column reads
`890883f4-8da5-474f-8f33-e803f2153c8a`, `8cc6fc4a-84a2-45d4-9c72-0c2c8e4fab06`,
`unattributed`.

After (`shot-spend-by-key-after.png`): `Unattributed`,
`orchestrator-livecheck (0fae3a)`, `lc2 (d9f834)`.

The masked tail is carried alongside the nickname because two keys may share a
nickname and the tail is what tells them apart. When the key list itself
cannot be read, the raw id stays on screen rather than every row being
labelled "Deleted key", which is asserted separately.

## #1406 — analytics scrolling sideways

The deployed page reproduces at 375 because its live data is wider than any
fixture here. This harness reproduces the identical mechanism at 320, a real
device width, with the same components and the same measurement.

Before (`shot-analytics-320-before.png`), viewport 320:

```text
window.scrollX after scrollTo(9999,0)   38
body.scrollWidth                        343
card "Cached vs uncached"               327
card "Top API keys by spend"            327
the six summary tiles                   273   (correct)
```

After (`shot-analytics-320-after.png`), viewport 320:

```text
window.scrollX after scrollTo(9999,0)   0
body.scrollWidth                        305
every card                              273
```

The two 327 cards are the shape the issue measured at 411 on the deployed
page: a grid item's default `min-width` is `auto`, which is its min-content
width, so one card that will not wrap sizes the whole auto track past the
container and every sibling stretches to match. `[&>*]:min-w-0` on both grids
removes that contribution. The same reasoning is already written down in
`components/ui/data-table.tsx` for tables.

`<main>` in `console-frame.tsx` was left alone: it is already a flex item of a
column that carries `min-w-0`, and the measurements above show the grid track
was the whole cause here. Widening that fix to every console route belongs
with #525.

## #1408 item 1 — blended price subtitle

Viewport 1440, same page as the #1406 capture.

Before (`shot-analytics-overview-1440-before.png`):

```text
161,794,930,349.395 credits. Credits spent divided by input plus output
tokens, per million. Effective, so cache reads are already priced in.
```

After (`shot-analytics-overview-1440-after.png`):

```text
161,794,930,349 credits per 1M tokens. Credits spent divided by input plus
output tokens. Effective, so cache reads are already priced in.
```

The three decimal places came from `Intl.NumberFormat`'s default, reached
through the generic `formatNumber`. Credits are integers everywhere else in
the product.

## #1402 — verdict, no code change

`docs/proof/qa-matrix-2026-08-29/03-console-feature-gates-stuck-saving.png`,
captured in the same QA pass that filed the issue, shows all twenty-five rows
rendering their real toggles in their real positions, six audit sinks off, four
sovereign gates on, and "Managed by your administrator" on the platform-only
rows. No "Saving…" is visible anywhere in that image.

`components/feature-gates/feature-gate-manager.tsx` keeps a `Saving…` span in
the DOM on every row at `opacity-0` and `aria-hidden="true"`, to reserve its
width so the row does not jump when a real save starts. `rowStatus` is `idle`
from first paint and only becomes `saving` inside `toggle()`. A text scrape
that walks `textContent` or `innerText` sees an `opacity-0` node, because
opacity is not visibility. That is what the issue's text dump recorded.

The distinction that matters for triage, since the two halves are both true:
the `Saving…` node **is** present in the DOM on every row, and therefore in
any text extraction, at all times. It is **not** visible to a person, and it
is **not** in the accessibility tree, so a screen reader does not announce it
either. The issue reported the first half as though it were the second.

PR #1394 did not fix it either: that change addressed `Viewer.TenantID`
resolution, whose failure mode on this page is the "Could not load feature
gates" empty state, not `Saving…`.

## Files

- `shot-api-keys-before.png`, `shot-api-keys-after.png`
- `shot-spend-by-key-before.png`, `shot-spend-by-key-after.png`
- `shot-analytics-320-before.png`, `shot-analytics-320-after.png`
- `shot-analytics-overview-1440-before.png`, `shot-analytics-overview-1440-after.png`

Posted to the pull request as permanent release assets through
`scripts/post-pr-visual-proof.sh`, not committed to git. Only this log is
committed, which is what `npm run lint:proof-tokens` scans.
