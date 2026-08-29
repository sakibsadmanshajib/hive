# Console defect batch: capture log

Branch: fix/console-defect-batch-2026-08-29
Date: 2026-08-29
Pull request: #1375

## Capture environment, and why it is a local render rather than the deployed console

The claims in this batch are all about how console components render, and two
of them (the dark-mode chart palette and the empty-chart state) are only
visible with a colour scheme and a data shape that the deployed console cannot
be put into on demand. A signed-in capture against the deployed console was
not available either: this machine's `.env` carries no Supabase URL, anon key
or service-role key, so `tests/e2e/support/live-auth.mjs` cannot mint a
session here, and the deployed console is running pre-fix code in any case.

The capture is therefore a real Next.js dev server, built from this branch's
tree into a private image tag (`hive-web-console:cheapwins-a5d2e7`, so no
shared tag was rebuilt under another agent), serving the real components
through the real `app/globals.css` pipeline. The only scaffolding is a
throwaway route mounted into the container at `app/proof/page.tsx` as a
read-only bind mount. It is not committed and it is not part of the diff: it
renders the shipped `ChartCard`, `UsageChart`, `SpendChart`, `ErrorChart` and
`DataTable` with sample rows so that all of the states can be seen at once.

The before/after pair is produced from the same image and the same route. The
"before" container additionally bind-mounts `origin/main`'s versions of
`usage-chart.tsx`, `spend-chart.tsx`, `error-chart.tsx`, `chart-card.tsx` and
`data-table.tsx` over the branch's, so the only difference between the two
captures is the five files this batch changes.

No URL in this capture carries a credential: the only address visited is
`http://localhost:3100/proof` (after) and `http://localhost:3200/proof`
(before).

## Commands

```
$ docker build -f deploy/docker/Dockerfile.web-console -t hive-web-console:cheapwins-a5d2e7 .
$ docker run -d --name hive-console-cheapwins \
    -e CONTROL_PLANE_BASE_URL=https://cp-hive.scubed.co \
    -e NEXT_PUBLIC_APP_URL=http://localhost:3100 \
    -e NEXT_PUBLIC_SUPABASE_URL=https://cp-hive.scubed.co \
    -e NEXT_PUBLIC_SUPABASE_ANON_KEY=stub-anon-key-for-local-render \
    -v <scratch>/proof-route/page.tsx:/app/apps/web-console/app/proof/page.tsx:ro \
    -p 3100:3000 hive-web-console:cheapwins-a5d2e7
$ curl -s -o /dev/null -w "%{http_code}" http://localhost:3100/proof
200
```

Screenshots were taken with Playwright driving the host's Chrome, one context
per colour scheme (`colorScheme: "light"` and `colorScheme: "dark"`, which is
what `app/globals.css` keys its dark palette on through
`prefers-color-scheme`), at a 1280 by 2820 viewport and `deviceScaleFactor: 2`.

## Capture transcript

```
[light] GET http://localhost:3100/proof status 200
[light] rendered series marks 2
[light] screenshot console-batch-light.png
[dark] GET http://localhost:3100/proof status 200
[dark] rendered series marks 2
[dark] screenshot console-batch-dark.png
```

Before, same route, origin/main components mounted over the branch's:

```
[light] GET http://localhost:3200/proof status 200
[light] screenshot console-batch-light.png
[dark] GET http://localhost:3200/proof status 200
[dark] screenshot console-batch-dark.png
```

## A capture trap worth recording

The first two attempts at this capture produced charts with axes, grid, legend
and tooltip but no plotted series at all, in both the before and the after
run, which looked like the colour change had broken every mark. It had not.
Two separate things were going on and both are worth knowing before anyone
shoots recharts again:

1. `fullPage: true` resizes the viewport to the document height before
   capturing. Recharts re-measures on that resize and restarts its entrance
   animation, so the shot lands before any series has been drawn. Capturing
   without `fullPage` at a viewport tall enough to hold the page fixes it.
2. Recharts does not draw its series until something triggers a measurement
   after mount in this headless context. A single `setViewportSize` nudge of
   one pixel, followed by a wait, is enough.

A DOM probe confirmed the colours were correct the whole time and only the
paint was missing: `path.recharts-curve` carried
`stroke="var(--color-accent)"` and a computed stroke of
`lab(66.4258 39.8986 37.0067)`, which is the resolved sienna accent, with a
real path `d` and `opacity: 1`. So `var()` in an SVG presentation attribute
does resolve in Chrome here, which is the assumption the chart-theme module
rests on.

## What the images show

`console-batch-dark.png` (after): all three chart panels sit on the dark
surface, series draw in sienna, green, red and neutral grey, axis labels and
legend text are legible against the dark page, the fourth card with no rows
renders "No activity in this time range yet." instead of a bare axis frame,
and the third table renders "Loading..." rather than "No records yet." while
its `loading` prop is set.

`console-batch-dark-before.png`: the same page with `origin/main`'s
components. Every chart panel is a white `#f9fafb` block punched into the dark
page, the empty chart renders an axis frame with nothing in it and no
explanation, and the loading table claims "No records yet."

`console-batch-light.png` (after): the light palette is unchanged in
character, which is the other half of the claim. The panel is the inset
surface rather than a hardcoded near-white, and the series colours come from
the same variables.
