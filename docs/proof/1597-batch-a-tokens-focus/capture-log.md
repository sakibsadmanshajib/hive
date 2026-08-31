# Capture log, issue #1597 batch A: the token bridge and focus

Date: 2026-08-31. Branch: `feat/1597-batch-a-tokens-focus`.

## Substrate

The screenshots are of the real `hive-open-webui:v0.10.2-branded` image built
from this branch, running as a container, signed in, in a real Chromium. Not a
static mock and not a server-rendered fragment.

```
cd deploy/docker && docker compose --env-file ../../.env --profile local build open-webui
docker run -d --name owui-proof-a17 -p 3399:8080 -e WEBUI_NAME=Hive ... hive-open-webui:v0.10.2-branded
```

The frontend build stage of that image is what compiles the two stylesheets
this batch changes, so a `@theme` mapping that Tailwind could not resolve would
have failed there rather than reaching a screenshot. It also runs
`npm run test:frontend -- --run` in place, which reported 329 passed, 22 of
them the new guard in `design-system-bridge.test.ts`.

### What was not reachable, and why

The documented full stack cannot start from this sandbox. `S3_ENDPOINT`,
`S3_ACCESS_KEY` and `NEXT_PUBLIC_SUPABASE_URL` are present in this machine's
`.env` with zero-length values, so `control-plane` and `edge-api` both exit at
boot on `storage unavailable`, and `open-webui` depends on both being healthy.
This is the state `.claude/skills/worktree-compose-stack.md` records as of
2026-08-29 and it is a configuration gap, not something waiting on a retry.

So the chat container was run standalone against its own SQLite, with
`ENABLE_SIGNUP` and `ENABLE_LOGIN_FORM` on and `OAUTH_AUTO_REDIRECT` off, and a
single throwaway local account was created through
`POST /api/v1/auths/signup`. The account address is `proof-local@example.invalid`
and its password was a fixed placeholder string, `<REDACTED>` here, on a
container bound to `127.0.0.1` that was destroyed after the capture. No shared
fixture account was touched and no password anywhere was rotated, per
`docs/live-test-auth.md`.

Consequences of running without `control-plane`, stated so nothing is claimed
beyond what the frames show: the model list is empty, so there is no live
transcript in these captures, and a settings modal with checkboxes in it was
not reached by the capture script. The checkbox change is therefore evidenced
from the compiled stylesheet rather than from a frame, below.

## Screens captured

Every screen was captured twice, once with `localStorage.theme = 'light'` and
`colorScheme: light`, once with both set to dark, at 1440 by 1000 and
`deviceScaleFactor: 2`.

| File | URL | What it shows |
|---|---|---|
| `01-signin-focused-<theme>` | `/auth` | A real email input with keyboard focus, so the ring is in frame on a control that previously carried `outline-none` |
| `02-chat-home-<theme>` | `/` | The whole chat surface: sidebar, greeting, composer, mode control, warm composer shadow |
| `03-mode-control-focused-<theme>` | `/` | Close up of the Chat and Cowork control: the new track, the selected thumb, its ring, and the focus ring |
| `04-destination-and-focus-<theme>` | `/artifacts` | Full frame with a current destination and a focused sidebar row |
| `05-sidebar-closeup-<theme>` | `/artifacts` | Close up of the sidebar: the 3px current-destination bar on Artifacts and the two-ring focus indicator on Projects |
| `10-projects`, `11-skills`, `12-artifacts`, `13-settings` | various | Blast-radius sweep, both themes, looking for damage from the radius, type, elevation and motion remapping |

## Contrast, measured rather than eyeballed

Every figure below was computed from the OKLCH declarations in
`packages/hive-tokens/tokens.css`, converted to sRGB and run through the WCAG
relative luminance formula. The requirement column is the criterion the pair
actually falls under: 1.4.3 for text, 1.4.11 for a non text indicator or a
graphical object.

| Pair | Need | Before | After |
|---|---|---|---|
| Focus ring on the light canvas | 3.00 | 2.67 | 4.50 |
| Focus ring on the light raised surface | 3.00 | 2.82 | 4.11 |
| Focus ring on the light sunken surface | 3.00 | 2.60 | 3.80 |
| Focus ring on the dark canvas | 3.00 | 5.34 | 5.34, unchanged |
| Focus ring on the dark surface | 3.00 | 4.60 | 4.60, unchanged |
| Current destination bar on the light canvas | 3.00 | 2.67 | 4.50 |
| Current destination bar on its own light row ground | 3.00 | 2.44 | 3.80 |
| Current destination bar on the dark canvas | 3.00 | 5.34 | 5.34, unchanged |
| Selected mode segment against the composer ground | 3.00 | 1.15 | 4.74 via its ring |
| Selected mode segment against its own track | 3.00 | no track existed | 3.80 via its ring |
| Selected mode segment, dark, against thumb and track | 3.00 | 1.19 | 4.60 and 5.92 |
| Placeholder ink on the light surface | 4.50 | 1.96 | 5.41 |
| Placeholder ink on the light canvas | 4.50 | 1.86 | 5.13 |
| Placeholder ink on the dark surface | 4.50 | 3.11 | 4.61 |
| Checkbox mark on the coral fill | 3.00 | 2.82 (white) | 4.60 (charcoal) |
| Unchecked checkbox boundary on the surface | 3.00 | 1.41 | 5.41 |

Two honest notes on that table.

The nav row's own **fill** is unchanged at 1.09:1 and is not the indicator. No
neutral in this light ramp can reach 3:1 against a 0.95 lightness canvas: it
would need roughly 0.62 lightness, which is a mid grey the brand does not
contain. `--hv-accent-soft` was measured as an alternative and is worse, 1.04:1.
So the bar carries 1.4.11 on that row and the fill stays a supporting cue. The
same reasoning applies to the mode control, where the thumb against the track
is 1.25:1 and the accent ring is what clears the bar.

The unchecked checkbox boundary is `--hv-ink-muted` rather than a border token
because no border token can serve as a form control boundary here:
`--hv-border` is 1.41:1 and `--hv-border-strong` 1.96:1 on the surface. That
ceiling is a systemic finding, recorded in `.wolf/decisions.md` D-058 for the
live brand as well, and widening the border ramp is not in this batch.

## Computed styles read from the live DOM

Full output in `computed-style-probes.txt` beside this file. These are
`getComputedStyle` values from the running page, not values inferred from the
stylesheet.

```
light  input:focus-visible  outline = 2px solid oklch(0.55 0.16 42), offset 2px
dark   input:focus-visible  outline = 2px solid oklch(0.678 0.164 43), offset 2px

light  .hv-mode             background-color = oklch(0.895 0.024 86)   (was: none declared)
light  .hv-mode             box-shadow       = oklch(0.76 0.032 85) inset 0 0 0 1px
light  .hv-mode-segment-on  background-color = oklch(0.968 0.011 85)   (was: 0.92 raised)
light  .hv-mode-segment-on  box-shadow       = oklch(0.55 0.16 42) inset 0 0 0 1px

dark   .hv-mode             background-color = oklch(0.195 0.005 92)
dark   .hv-mode-segment-on  background-color = oklch(0.288 0.005 107)
dark   .hv-mode-segment-on  box-shadow       = oklch(0.678 0.164 43) inset 0 0 0 1px

light  .hv-nav-row-active::before  width = 3px, background = oklch(0.55 0.16 42)
dark   .hv-nav-row-active::before  width = 3px, background = oklch(0.678 0.164 43)

light  --hv-focus-ring resolves to oklch(.55 .16 42)
dark   --hv-focus-ring resolves to oklch(.678 .164 43)

body font-family = "Hanken Grotesk", "Noto Sans Bengali", ui-sans-serif, ...
```

The last two lines together are the whole of issue #1521's confirmed half: one
token, two themes, correct in both, where before it was one literal that was
right in dark and silently wrong in light.

## The compiled stylesheet, verified rather than assumed

Read out of `/app/build/_app/immutable/assets/0.DpCQCyn-.css` in the built
image, which is the file the browser actually loads.

The theme namespaces were taken from the installed `tailwindcss@4.2.1`'s own
`theme.css`, extracted from that exact version rather than recalled, because
several of these names moved between v3 and v4.

```
--default-transition-duration        : var(--hv-duration-fast)
--default-transition-timing-function : var(--hv-ease-out)
--ease-out                           : var(--hv-ease-out)
--font-mono                          : var(--hv-font-mono)
--radius-2xl                         : var(--hv-radius-md)     16px becomes 12px
--radius-md                          : var(--hv-radius-sm)     6px becomes 8px
--text-2xl                           : var(--hv-display-sm)    24px becomes 28px
--text-md                            : var(--hv-text-md)       15px, new step
--shadow-lg                          : 0 6px 16px -6px oklch(19% .006 260/.12), ...

.rounded-2xl { border-radius: var(--radius-2xl) }
.transition  { transition-timing-function: var(--tw-ease,var(--default-transition-timing-function));
               transition-duration: var(--tw-duration,var(--default-transition-duration)) }
.shadow-lg   { --tw-shadow: 0 6px 16px -6px var(--tw-shadow-color,oklch(19% .006 260/.12)), ... }
```

That `.shadow-lg` line is why the elevation entries are literal values rather
than `var(--hv-shadow-N)` references like everything else in that block.
Tailwind parses a shadow value in order to splice `var(--tw-shadow-color, ...)`
into it, which it managed here and could not have done through a reference. And
the dark blocks in `tokens.css` resolve the first three shadow tokens to `none`,
which is legal in a `box-shadow` only as the sole value, so a reference would
have invalidated the whole five-slot declaration in dark and taken every
`ring-*` utility on the same element down with it.

### The focus rule beats `outline-none` for a structural reason, and it was checked

`outline-none` is a Tailwind utility and therefore sits inside
`@layer utilities`. The blanket rule is in `hive.css`, which is unlayered, and
an unlayered declaration outranks a layered one whatever their specificities.
Measured on the compiled file by counting layer nesting depth at each rule's
byte offset:

```
layer nesting depth at .outline-none      : 1
layer nesting depth at blanket focus rule : 0
```

The rule's own specificity is (0,2,0), a deliberate tie with the component
focus rules further down `hive.css` which it sits above, so `.hv-chip` and
`.hv-notfound-action` keep their two-ring treatment on source order. The audit
proposed `[tabindex]:not([tabindex='-1'])`, which is (0,3,0) and would have
outranked those rules and stacked a second ring on them; `[tabindex='0']` is
used instead, and a scan of the tree found only `tabindex="0"` and
`tabindex="-1"` in use, so nothing is lost.

### Checkboxes, the one claim with no frame behind it

```
input[type=checkbox]:checked { background-color: var(--hv-accent);
                               border-color: var(--hv-accent) }
```

No `blue-` anywhere in the checkbox region of the compiled file, the checkmark
data URI carries `stroke="%232b2b28"` (charcoal, 4.60:1 on coral) and no longer
`stroke="white"` (2.82:1, which fails 1.4.11 for a graphical object).

## Coverage of `outline-none`

The audit counted 131 sites in `lib/components/chat` and `lib/hive`. Counted
across the whole of `vendor/open-webui/src`, there are 576 occurrences of
`outline-none` or `outline-hidden` in 140 files. Because `hive.css` is loaded
once from `routes/+layout.svelte` and the rule is unlayered and global, all 576
are covered, not only the 131 the audit scoped.

## Blast-radius sweep

The token bridge changes components this batch never opened, so `/projects`,
`/skills` and `/artifacts` were captured in both themes and compared against
what the audit describes. Nothing regressed: the panel heads, search fields,
cards, empty states and pill buttons all still read correctly, and the 4px
radius shifts are not visible as damage anywhere in frame.

What was **not** covered, stated plainly rather than implied:

- No live transcript, no streaming state and no code block, because no model is
  configured without `control-plane`. `rounded-2xl` at 12px inside a message
  bubble is therefore unverified visually.
- No settings modal, no model selector dropdown and no other popover, so the
  elevation remap on `shadow-lg` is verified on the composer only.
- No 375px mobile capture.
- There is no visual regression harness in this repo, so none of this is
  automated and none of it will catch the next drift. The audit's own closing
  section says so; a token conformance lint plus one Playwright screenshot per
  destination per theme is the thing that would.
