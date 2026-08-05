# Visual proof: launcher clearance at 768px, 2026-08-05

Proof for the fix to `09-agent-workspace-launcher.spec.ts:72`, the last hard
failure in the OWUI nightly (runs on `dfc50f73` and the two before it).

## What actually failed

Not the geometry. The spec never reached a bounding box:

```
Error: OWUI model selector at 1440px
expect(locator).toBeVisible() failed
Locator: locator('button[aria-label="Select a model"]')
Expected: visible
Error: element(s) not found
  at 09-agent-workspace-launcher.spec.ts:114:7
```

It failed on the first width of the loop, 1440px, on the wait that exists so the
clearance check does not measure a half-built header. Open WebUI labels that one
button two different ways: `Select a model` while nothing is selected, and
`Selected model: <id>` once something is (upstream i18n keys `Select a model` and
`Selected model: {{modelName}}`). The spec matched the first. That was never a
correct handle on the selector, only one that happened to hold while the e2e
catalogue came back empty. Once #735 and #737 made models resolve, Open WebUI
auto-selected one and the label the spec waited for stopped existing.

The failing run's own page snapshot shows it: `button "Selected model: hive-auto"`.

## The fix

Match the id upstream depends on itself. The pinned v0.10.2 bundle sets
`id="model-selector-<n>-button"` unconditionally, in the same update block as
`aria-expanded`, and its own model-selector keyboard shortcut is a literal
`document.getElementById("model-selector-0-button").click()`. So the id is
present in both states, and cannot be renamed upstream without breaking upstream.

Verified in both states, not assumed:

| Catalogue | `aria-label` | `id` |
|-----------|--------------|------|
| serving `hive-auto` | `Selected model: hive-auto` | `model-selector-0-button` |
| empty (`/api/models` stubbed to `[]`) | `Select a model` | `model-selector-0-button` |

`0768-header-empty-catalogue.png` is that second row.

## The geometry the spec then measured

The CSS is correct. With the selector's text replaced by the 44-character
OpenRouter-style id the spec uses as its worst realistic case
(`meta-llama/llama-4-maverick-17b-128e-instruct`), measured live at each width
the spec walks:

| Viewport | Selector right | Group right (with add-model) | Launcher left | Clearance | Overlaps |
|----------|----------------|------------------------------|---------------|-----------|----------|
| 1440px | 498 | 520 | 1163 | 643 | none |
| 1024px | 498 | 520 | 747 | 227 | none |
| 900px | 498 | 520 | 746 | 226 | none |
| 768px | 498 | 520 | 614 | 94 | none |

498 at 768px is exactly the figure `custom.css`'s measurement table already
carried, so nothing about the placement has drifted. Two things that table did
not account for are now noted in it: those right edges are the selector's own and
the add-model button adds about 22px past them (hence 520, and 94px of real
clearance at 768px rather than 116px), and selecting a model makes upstream
render a "Set as default" button, which sits on its own line below the selector
and never enters the launcher's row.

| File | Shows |
|------|-------|
| `0768-header-natural.png` | 768px, model selected, launcher present and clear |
| `0768-header-long-model-id.png` | Same width with the 44-character id in the selector. This is the state the clearance assertion measures |
| `0768-full-long-model-id.png` | Whole viewport at 768px, so a header crop cannot hide a problem elsewhere |
| `1440-header-natural.png`, `1440-header-long-model-id.png` | The width that actually failed in the nightly |
| `0768-header-empty-catalogue.png` | Empty catalogue, placeholder label, same button id |
| `0375-header-absent.png` | 375px, deliberately no launcher |

## Proof the check still has teeth

A passing overlap assertion proves nothing on its own, so the same rig forced a
real collision: `#hive-agent-launcher { right: 300px }` at 768px with the long id
in place, which drops the launcher onto the selector's text.

`0768-header-negative-control-forced-overlap.png` shows the icon sitting on top
of `...instr(o)ct`, and the spec's own overlap query reported
`["Selected model: hive-auto"]` instead of `[]`. The assertion catches a genuine
overlap and names the element it hit.

## How these were captured

Playwright 1.58.2 against the branded image the compose stack runs,
`hive-open-webui:v0.10.2-branded`, built from `deploy/docker/Dockerfile.open-webui`
at this branch's HEAD, with `deploy/docker/owui-static` bind-mounted at
`STATIC_DIR` exactly as `docker-compose.yml` mounts it, so `loader.js` and
`custom.css` are the real files. Signed in through Open WebUI's own auth.

The model list came from a local stub serving one model instead of from
edge-api. That is deliberate and it is the whole surface under test here: the
launcher is a `<body>`-level fixed element and the header is upstream markup, so
the only thing the rest of the stack contributes to this geometry is the model id
string, which the spec overwrites with its own 44-character worst case anyway.
The rig's header rendered node-for-node identically to the failing nightly's page
snapshot, down to `Selected model: hive-auto`, `Add Model` and `Set as default`,
and reproduced the nightly's failure exactly before the fix: same assertion, same
1440px width, same `element(s) not found`, with the file's other three tests
passing. No URL in any capture carries a credential.
