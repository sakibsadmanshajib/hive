# Visual proof capture log: Usage tab, populated state and enterprise absence

PR: feat/chat-settings-usage-tab, second capture. Captured 2026-08-29.

Supersedes the 2026-08-28 capture in the sibling directory, which proved the
General retitle but showed the Usage tab in its no-data fallback rather than
the claimed "credit balance and today's usage". That gap was the review
finding this capture closes.

## What is proven

Three screenshots, taken with Playwright (chromium, 1280x860) against a real
container built from this branch with the shipped
`deploy/docker/Dockerfile.open-webui`, reached over plain HTTP on localhost.
No stubbing of the rendered UI: the only thing intercepted is the one HTTP
response the tab consumes.

- `20-settings-general.png`: Settings, General tab. Header reads "Chat
  Preferences", the retitle. Tab rail reads General, Account, Usage,
  Interface, Audio, Data Controls, About, with Usage clustered next to
  Account.
- `21-settings-usage.png`: Settings, Usage tab, populated. "Organization
  credit balance $12.50", "Organization usage today $0.34", the "Top up"
  link, a "Last updated" stamp and the "Refresh" control. Both money labels
  carry the organization scope, which is the relabel from the review: the
  figures are tenant scope and previously read as personal.
- `22-settings-enterprise.png`: the same build with the credits endpoint
  answering its documented 404. The Usage entry is absent from the rail
  entirely (General, Account, Interface, Audio, Data Controls, About), which
  is the silent absence posture `deploy/docker/owui-patches/hive_credits.py`
  describes. Before this change the tab was present and permanently empty.

## How the populated state was produced without a database

Issue #1297 leaves this sandbox with no reachable Supabase, so the real
`internal/chat/credits/balance` chain cannot run here. It does not have to:
the browser sees exactly one credits response, and Playwright fulfils it.

```js
await page.route('**/hive/credits/balance', async (route) => {
  await route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      available_credits: 12500000000,
      usage_today_credits: 340000000,
      top_up_url: 'https://console-hive.scubed.co/console/billing'
    })
  });
});
```

The two magnitudes are deliberately different, so the screenshot also shows
the figures landing in the right rows: 12,500,000,000 credits is $12.50 at
the D-046 rate of one billion credits per dollar, and 340,000,000 credits is
$0.34. A transposition would be visible in the image itself. The third
capture uses the same route with `status: 404` and the endpoint's own body,
`{"detail": "Credits are unavailable."}`.

The panel's rendered text, read back from the live DOM in the same run:

```
Usage | Organization credit balance | $12.50 | Organization usage today |
$0.34 | Top up | Last updated: 11:07:13 PM | Refresh
```

Tab rails, read from the live DOM in the same run:

```
credits available: ["General","Account","Usage","Interface","Audio","Data Controls","About"]
credits absent:    ["General","Account","Interface","Audio","Data Controls","About"]
```

## Sign-in path used

Not the shared QA fixture and not the production OAuth flow. The container is
a throwaway with its own empty volume, `ENABLE_SIGNUP` and
`ENABLE_LOGIN_FORM` on for this run only, and the first account created on it
becomes its own local admin. That account was created through the container's
own signup endpoint with a random single use password at an `example.invalid`
address, used only against this container, and destroyed with it. Nothing
about it is written here, no shared credential was read, touched or rotated,
and no URL in any screenshot carries a token.

## Build note, environment rather than code

The image is the shipped Dockerfile with one substitution: the frontend build
step runs `npx vite build` instead of `npm run build`, because
`npm run pyodide:fetch` cannot reach the pyodide CDN from this sandbox and
fails the build outright (`fetch failed`, `SocketError: other side closed`,
observed twice). Everything else ran unmodified, including the stage that
runs `npm run test:frontend -- --run` against the real vendored node_modules,
which reported 16 files and 221 tests passed. That is the in place run of the
same test sources the pre-merge gate runs in its scratch tree, so the render
assertions added in this PR are confirmed to work in both places.

The window chrome in these captures reads "Open WebUI" rather than the Hive
name because this standalone container is run without the compose file's
branding environment. It has no bearing on what is being proven.

## Test evidence backing the same change

`make test-owui-frontend`, the pre-merge gate, on the unmutated tree:

```
Test Files  16 passed (16)
     Tests  221 passed (221)
14/14 components compiled
```

The same gate, with each of the reviewer's three mutations applied one at a
time: compile error red, transposed figures red on three assertions, emptied
click handler red on one assertion, exit 2 in every case.

## Recaptured after the second review round

The screenshots above were first taken at commit `6a30d39`, then retaken
unchanged in appearance at `338b699`, the head that answers the second review
round. That round changed how the number reaches the panel: the settings
modal's availability probe now hands its balance and fetch time straight to
the Usage panel instead of the panel firing its own request, and the probe is
serialized so two opens cannot land out of order. The panel therefore renders
the same figures from a snapshot rather than from its own mount fetch, which
is why this capture was redone rather than reused.

Same run, read back from the live DOM at that head:

```
Usage | Organization credit balance | $12.50 | Organization usage today |
$0.34 | Top up | Last updated: 11:30:24 PM | Refresh
```

The image build for that container reported `Test Files 16 passed (16)` and
`Tests 221 passed (221)` from its in place `npm run test:frontend -- --run`
stage, against the same commit.
