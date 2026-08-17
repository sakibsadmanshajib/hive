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

## Dependency advisories, triaged once

Frontend npm advisories are ours now, because the lockfile is. `npm audit` against the
vendored `package-lock.json` reported 29 advisories at the time this tree landed: 1 critical,
15 high, 12 moderate, 1 low. The point of this section is that the next reader does not repeat
the analysis.

**The critical one and most of the high cluster do not ship.** They sit under `vitest`,
`vite`, `vite-node`, `cypress`, `esbuild`, `extract-zip`, `@cypress/request` and `uuid`, all
of which are `devDependencies`. `npm run build` emits a static bundle; none of that tooling is
in it, and none of it runs in this image. Left alone deliberately.

Four are direct `dependencies`, so they need an answer rather than a shrug. Reachability was
read in this tree, not assumed:

| Package | Ships in the bundle | Reachable how | Fix upstream | Verdict |
|---|---|---|---|---|
| `xlsx` | Yes | `src/lib/utils/excelToTable.ts` dynamically imports it to parse a spreadsheet the user uploaded | **None available.** The long unpatched SheetJS package | The one that actually matters. Prototype pollution and ReDoS against attacker supplied input. Needs a real decision: replace the parser, or move spreadsheet parsing server side. Not a doc note's job to decide |
| `dompurify` | Yes | Imported by a dozen components, and it is the sanitiser this application trusts for rendered content | Available | Upgrade on its own, with the XSS-relevant call sites exercised. Deliberately not bundled into the fork commit |
| `undici` | **No** | Only `scripts/prepare-pyodide.js`, a build time Node script that fetches Pyodide wheels | Available | Build time only, on our own builder. Upgrade with the next lockfile touch |
| `sharp` (via `@huggingface/transformers`, `kokoro-js`) | **No** | Native Node module behind the transformers package's Node backend. Verified: the built output contains no import of it | None available | Not reachable in a browser build. No action |

Nothing above was upgraded in the commit that vendored this tree, on purpose: a dependency
bump inside the same change that introduces 4,900 files is unreviewable, and three of the four
need their own verification.

**Nothing is watching this tree yet.** There is no `.github/dependabot.yml` anywhere in this
repository. That gap predates the fork, but the fork is what makes it expensive, so wiring
Dependabot at `vendor/open-webui` is the follow-up that keeps this section from going stale.

## What has not been retired yet

The bundle rewrites in `deploy/docker/owui-patches/` still run, and they run **against the
stock bundle that ships inside the pinned upstream image, not against our own build**. The
ordering in `Dockerfile.open-webui` is deliberate and is the whole reason they still pass:
every rewrite executes earlier in the final stage, then `rm -rf /app/build` plus
`COPY --from=frontend` discard that bundle and replace it with the Hive source build, last.
The rewrites therefore never see the Hive-built output at all.

That matters for whoever retires this layer. What the rewrites protect today is **not** the
bundle we ship: it is a drift check on upstream. They fail the build if a future image digest
moves the stock bundle out from under the source tree we track. Every surface they remove is
removed again, independently, in the vendored source, and that source removal is what actually
reaches a user.

The reason they cannot simply be pointed at our own build: those rewrites match on minified
identifiers, which the minifier allocates per chunk, so any edit to our source renames them.
Measured on the first build of this stage, three of nineteen stopped matching surfaces that
had not come back, and an identifier-tolerant pattern for two of them then matched sibling
components that must not be touched.

Replacing them with source deletions is the natural next change and it is deliberately not in
this one: another change was in flight against `hive_ui_surfaces.py` at the time, and two
edits to that file from different directions is how a surface silently comes back.
