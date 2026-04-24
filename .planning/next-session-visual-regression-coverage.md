# Next Session: Add visual regression coverage to CI

## Scope

Close the gap that allowed the v1.0 web-console to ship with zero CSS: no CI step inspects visual output. This session adds a visual-regression / screenshot-diff check that would have flagged the unstyled console.

**Do not start this session until the UI styling session completes.** Visual-regression needs a styled baseline to diff against.

## Gap being closed

Current CI (`ci.yml`):
- `web-unit` — `tsc --noEmit`, vitest, `next build`. Build passes with zero CSS.
- `web-e2e` — Playwright functional tests: click, nav, auth flow. No visual assertions.
- No screenshot comparison, no designqc, no Lighthouse-style visual score.

Result: an unstyled console ships green.

## Goal

Add one of:

**Option A — Playwright screenshot comparison** (preferred, already in repo):
- Add a `tests/visual/` spec in `apps/web-console/tests/` that navigates to 6-10 key routes (sign-in, console dashboard, billing, api-keys, catalog, analytics) and calls `expect(page).toHaveScreenshot(...)`
- First run locally generates baselines under `tests/visual/__screenshots__/`
- Commit baselines
- CI re-runs and diffs; fails on > N% pixel diff

**Option B — openwolf designqc gated step**:
- Add workflow step that boots Next.js, runs `openwolf designqc --routes /auth/sign-in /console ...`, uploads screenshots as artifact
- No automatic diff (designqc captures only); adds review friction but catches "no CSS" class regressions since empty pages look visibly off

**Option C — Lighthouse CI visual score** (lowest signal, skip)

Recommendation: **A + B**. A is the automated gate; B uploads artifacts for humans.

## Step-by-step (Option A)

1. Wait for UI styling session to complete and merge
2. Branch: `ci/web-console-visual-regression`
3. Add `apps/web-console/tests/visual/layout.spec.ts` with `toHaveScreenshot` assertions for:
   - `/auth/sign-in`
   - `/auth/sign-up`
   - `/console` (authed, uses existing Playwright fixture)
   - `/console/billing`
   - `/console/api-keys`
   - `/console/catalog`
4. Configure `playwright.config.ts` — `expect.toHaveScreenshot: { maxDiffPixelRatio: 0.02 }` (2% tolerance)
5. Run locally to generate baselines: `npx playwright test tests/visual --update-snapshots`
6. Commit baselines (they live under `tests/visual/__screenshots__/`)
7. Modify `web-e2e` job to run visual spec alongside existing specs
8. Modify upload-artifact step to include `tests/visual/__screenshots__/` on failure
9. PR title: `ci(web-console): add visual regression screenshot tests`
10. PR body: baseline screenshots inline, diff threshold reasoning

## Constraints

- Baselines are binary PNGs — commit to repo (or LFS if they blow up size)
- Tolerance must be tuned: too strict = flaky on font rendering differences, too loose = catches nothing
- Run in `chromium` only for baselines (firefox/webkit add flake without much signal)
- NEVER push directly to main
- Do not merge until UI styling has landed and baselines are stable

## Files to add

- `apps/web-console/tests/visual/layout.spec.ts` (new)
- `apps/web-console/tests/visual/__screenshots__/**/*.png` (new, committed)
- `apps/web-console/playwright.config.ts` (edit — toHaveScreenshot threshold)
- `.github/workflows/ci.yml` — web-e2e step already runs all specs, may need artifact upload tweak only

## Out of scope

- Cross-browser visual testing (add after baseline stable)
- Percy/Chromatic SaaS (not worth the spend at this stage)
- Visual regression on the API surface (already covered by SDK goldens)
