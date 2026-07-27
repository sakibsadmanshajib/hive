# Visual proof: agent-workspace launcher native polish, 2026-07-27

Before and after screenshots for the change described in the pull request that
added this directory. Captured with Playwright against the real stack running
from `deploy/docker/docker-compose.yml` (`--profile local --profile chat`),
signed in against Open WebUI's own login, not mocked and not hand-drawn.

Every `-BEFORE.png` was taken by checking `origin/main`'s copies of
`deploy/docker/owui-static/loader.js` and `custom.css` back into the running
stack (both are bind-mounted, so no rebuild is involved) and re-shooting the
same viewport. Same browser, same session, same stack, one variable.

## Native parity

| File | Shows |
|------|-------|
| `parity-idle-1440px-light-BEFORE.png` / `parity-idle-1440px.png` | The launcher beside Open WebUI's own header icons. Before: a white bordered pill with a drop shadow, matching nothing around it. After: transparent, `rounded-xl`, Open WebUI's own grey, indistinguishable in treatment from the three icons to its right |
| `parity-hover-owui-native.png` / `parity-hover-hive-launcher.png` | Hover state on Open WebUI's own Controls button and on the launcher. Both settle on `oklch(0.98 0 0)`, asserted equal in the spec rather than eyeballed here |
| `dark-1440px-header-BEFORE.png` / `dark-1440px-header.png` | Same comparison in Open WebUI's dark theme |
| `dark-768px-header.png`, `dark-768px-full.png` | Icon-only form in dark theme |

Measured values, native versus launcher, both read off the live page: padding
`8px`, border-radius `12px`, border-width `0px`, background
`rgba(0, 0, 0, 0)`, colour `oklch(0.51 0 0)` light and `oklch(0.77 0 0)` dark,
transition `0.15s`, icon 18px stroked in `currentColor`. Before this change the
same properties read `0px 12px`, `8px`, `1px`, `rgb(255, 255, 255)`,
`rgb(23, 23, 23)`, `120ms` and a 16px icon.

## Viewport coverage

| File | Shows |
|------|-------|
| `1440-1440px-header-BEFORE.png` / `1440-1440px-header.png` | Labelled form, restyled |
| `1024-1024px-header-BEFORE.png` / `1024-1024px-header.png` | Labelled form at the low end of the label gate |
| `0900-900px-header-BEFORE.png` / `0900-900px-header.png` | **Before: nothing at all.** After: icon-only, sitting as the first of four header icons |
| `0768-768px-header-BEFORE.png` / `0768-768px-header.png` | Same, at the new floor |
| `0375-375px-header-BEFORE.png` / `0375-375px-header.png` | Unchanged and deliberately absent, for the reason below |
| `*-header-model-selected*.png` | The same widths with the seeded default model id (`route-openrouter-default`) in Open WebUI's selector, which is what actually consumes the header's free span |
| `*-full.png` | Whole viewport at each width, so the header crops cannot hide a layout problem elsewhere |

Why the floor is 768px and not 375px: Open WebUI's model selector is unclamped
(`max-width: none`), so its left-hand header group grows with the length of the
selected model's id. Measured right edge of that group against the launcher's
left edge:

| Viewport | Unselected | Seeded default (24 chars) | Longest realistic OpenRouter id (44) | Launcher left edge |
|---|---|---|---|---|
| 1440px | 237 | 327 | 520 | 1163 |
| 1024px | 237 | 327 | 520 | 747 |
| 768px | 237 | 327 | 498 | 614 |
| 640px | 218 | 308 | 479 | 486 |
| 600px | 218 | 308 | 458 | 446 |
| 480px | 218 | 286 | 338 | 326 |
| 375px | 196 | 233 | 233 | 221 |

768px clears a 44-character id by 116px. 640px clears it by 7px, and from 600px
down the two collide. A collision is not cosmetic here: the launcher paints above
Open WebUI's own controls, so it would swallow clicks meant for the model
selector. Going below 768px needs Open WebUI to clamp that selector, which means
a fork or an upstream fix, so below 768px the per-message "Open Agent Workspace"
Action stays the entry point exactly as before.

## Client-side navigation and the token race

| File | Shows |
|------|-------|
| `spa-notes.png`, `spa-workspace.png`, `spa-back-on-chat-root.png` | `/` to `/notes` to `/workspace/models` and back, all SvelteKit client-side routing with zero document loads. Launcher present exactly once at every hop |
| `spa-settings-modal.png` | Open WebUI's settings modal open over the chat surface, launcher still present once |
| `spa-final-exactly-one.png` | End state of that sequence |
| `late-signin-01-signed-out-after-15s.png` | Sign-in screen after 15s. No launcher, which is correct: it would only lead to a second sign-in prompt |
| `late-signin-02-header-BEFORE.png` / `late-signin-02-header.png` | Signing in after that 15s wait. **Before: no launcher, for the rest of the session.** After: present |
| `late-signin-02-BEFORE.png` / `late-signin-02-launcher-present.png` | Full-viewport version of the same pair |

Open WebUI signs in without a document load, so `loader.js` never runs again and
its poll is the only thing that can notice. The original ~6s ceiling therefore
lost the launcher for anyone who spent longer than six seconds on the sign-in
screen. Sign-out needs no equivalent handling because it does trigger a full
document load, also verified.

## Capture notes, so the conditions are on the record

- Signed in through Open WebUI's own email and password form, against a local
  throwaway admin account in the local Open WebUI SQLite database. No seeded
  Hive owner and no OAuth round trip were needed for header chrome.
- **The model list was empty for this run**, so no live model completion was
  exercised: the Hive shim rejects this local Open WebUI account because it has
  no tenant mapping, which is an environment data gap rather than anything this
  change touches. The launcher is not on the completion path. Where a selected
  model id matters, it matters only for header geometry, so the
  `-model-selected` shots set the selector's label directly to the seeded
  default and to longer ids, which is what the measurement table above is built
  from.
- Client-side navigation was proven with real Open WebUI route changes plus its
  settings modal, each asserted against a `load`-event counter, rather than with
  a chat send that could not complete without a model.
- `apps/web-console/e2e/phase-19/owui/09-agent-workspace-launcher.spec.ts`
  encodes all of the above as assertions. Verified in both directions: 4 passed
  against this change, and the late-sign-in test fails with
  `element(s) not found` when `loader.js`'s retry schedule is reverted to
  `origin/main`'s.
