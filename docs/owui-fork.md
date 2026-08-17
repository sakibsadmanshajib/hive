# The Open WebUI fork

`vendor/open-webui` is the upstream Open WebUI repository at tag `v0.10.2`, added as a
squashed git subtree. Hive builds its frontend from that source and keeps upstream's Python
backend from the pinned image.

Owner decision 2026-08-11 (`.wolf/decisions.md` D-036): Open WebUI is forked and heavily
modified. The exact-literal bundle rewrites under `deploy/docker/owui-patches/` and the
static hook `deploy/docker/owui-static/custom.css` are the old ceiling, not a rule, and
nothing in them may be cited to refuse fork work.

## What is ours

| Path | What |
|---|---|
| `packages/hive-tokens/tokens.css` | Design tokens. Plain CSS custom properties, no framework, shared with the React applications. Outside the vendored tree on purpose, so the visual identity survives a change of chat engine |
| `vendor/open-webui/src/lib/hive/` | Every Hive authored component and stylesheet in the fork |
| `vendor/open-webui/src/routes/(app)/agents/` | The agent workspace as a destination in the shell |

Edits to upstream files are kept to insertion points a rebase can replay, and there are four:

| File | Edit |
|---|---|
| `src/tailwind.css` | The neutral ramp plus white and black, remapped to the brand's warm palette. Every surface and ink in the application reads from these fourteen values |
| `src/routes/+layout.svelte` | One import of `$lib/hive/hive.css`, last, so it wins over the upstream defaults it replaces |
| `src/lib/components/layout/Sidebar.svelte` | One import and two `<HiveShellNav />` insertions, one per sidebar state |
| `src/app.html` | The `/static/loader.js` hook removed with the overlay it injected |

## How it is built

`deploy/docker/Dockerfile.open-webui` gained a first stage that compiles the vendored
frontend the way upstream's own Dockerfile does (`node:22-alpine3.20`, `npm ci --force`,
`npm run build`) and copies the result over `/app/build` in the pinned image. The backend,
its dependency set and every existing backend patch are untouched.

The stage asserts that the vendored `package.json` version equals the version inside the
pinned image. A digest bump without a matching subtree pull fails the build rather than
serving a frontend against a backend whose endpoints have moved.

Measured cold on a development machine: `npm ci` about 90 seconds, `npm run build` about 420
seconds. Warm, with `vendor/open-webui` untouched, both layers are cache hits.

## Taking upstream changes

```bash
git subtree pull --prefix vendor/open-webui https://github.com/open-webui/open-webui <tag> --squash
```

Frozen frontend, tracked backend: upstream frontend features are not taken, backend fixes
arrive by bumping the image digest, and when a digest bump crosses a release the subtree is
pulled to the matching tag and the four edits above are replayed.

Frontend npm advisories are ours now. `vendor/open-webui` belongs in Dependabot's scope.

## What has not been retired yet

The bundle rewrites in `deploy/docker/owui-patches/` still run, against our own build rather
than against a prebuilt one, and every one of their nineteen literal rewrites still matches
byte for byte because the same source and the same lockfile minify the same way. Replacing
them with source deletions is the natural next change and it is deliberately not in this one:
another change was in flight against `hive_ui_surfaces.py` at the time, and two edits to that
file from different directions is how a surface silently comes back.
