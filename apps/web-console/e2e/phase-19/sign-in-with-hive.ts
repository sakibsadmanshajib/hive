import { expect, type Page } from "@playwright/test";

// The one implementation of the "Continue with Hive" journey into Open WebUI.
//
// It used to exist twice. owui.setup.ts had this version, which follows the
// real OIDC chain through the consent screen. e2e/phase-19/auth.setup.ts had a
// second, older copy that clicked a "Next" button, then a "Sign in" button,
// and then asserted the browser had landed back on the Open WebUI origin, with
// no consent step at all. That copy encoded the sign-in UI as it was before
// the consent flow existed, so it walked the browser as far as the console
// origin and stopped there. The assertion it failed on read
// `Expected "http://localhost:3003", Received "https://console-hive.scubed.co"`,
// which looks like a redirect bug in the product and is nothing of the sort.
//
// A duplicated journey is a stale journey waiting to happen: the copy that is
// exercised nightly gets fixed, and the copy that no workflow runs does not.
// One implementation, two callers, so the next sign-in change breaks in one
// place and is fixed in one place.
export async function signInWithHive(
  page: Page,
  email: string,
  password: string,
  owuiOrigin: string,
): Promise<void> {
  // ponytail: OWUI login page has a continuously animating element, so
  // Playwright's click-stability check never settles, and its force-click still
  // requires the element in the viewport, which fails during the same
  // animation. dispatchEvent fires the DOM click handler directly, regardless of
  // geometry, stability, or overlays.
  const hiveButton = page.getByRole("button", { name: /continue with hive/i });
  await expect(hiveButton).toBeVisible({ timeout: 30_000 });
  await hiveButton.dispatchEvent("click");

  // The OAuth click starts a real full-page redirect chain: OWUI -> Supabase
  // authorize -> /oauth/consent (web-console origin, unauthenticated) ->
  // /auth/sign-in?next=... (still web-console origin). The consent and
  // sign-in pages live on the web-console's Supabase Site URL, a different
  // origin from OWUI_URL, so this helper never calls page.goto with a relative
  // path past this point -- it only follows whatever the browser is
  // redirected to, which keeps it baseURL-agnostic.
  // getByLabel("Password") is a strict-mode violation here: the browser's
  // native password-reveal toggle button shares "Password" in its
  // accessible name. getByRole("textbox", ...) excludes it by role.
  const emailBox = page.getByRole("textbox", { name: /email/i });
  const passwordBox = page.getByRole("textbox", { name: /password/i });
  const approveButton = page.getByRole("button", { name: /approve/i });
  // Run 28681926134: consent loads first (200), then its client-side
  // session check bounces to sign-in (200) -- a URL/pathname check can
  // observe the transient consent pathname and wrongly decide login is
  // done. Wait on the DOM instead: either we need to sign in (email box
  // visible) or we're already on consent (Approve visible).
  // Wait for the consent app to be serving before timing the login.
  //
  // The consent and sign-in pages are web-console's, on a different origin,
  // and web-console runs in dev mode here (see the note below), so it compiles
  // its routes on first request. Measured on run 32112158077: the first two
  // attempts never left the Open WebUI origin and timed out at 30s, and the
  // third completed the whole exchange in 21 seconds once the route was warm.
  // That is a one-off compile at the start of a run, not a slow login.
  //
  // The budget here is deliberately NOT the login budget, and the login budget
  // below is deliberately unchanged. Raising the 30s assertion would have
  // buried a cold compile inside a per-login timeout and made every future slow
  // login indistinguishable from this one, which is the failure that hides the
  // next real regression.
  //
  // This adds no new failure mode: the assertion on the next line already
  // requires a DOM that only exists on the consent origin, so requiring the
  // browser to reach that origin first is strictly weaker than what was
  // already being demanded.
  await page.waitForURL((u) => u.origin !== owuiOrigin, { timeout: 120_000 });

  await expect(emailBox.or(approveButton)).toBeVisible({ timeout: 30_000 });

  // web-console runs in dev mode in CI; React hydration can remount the
  // controlled inputs *after* a fill already verified as stuck, wiping
  // them, so a submit fired after that remount hits an empty form (run
  // 28680373668: "missing email or phone" alert, both textboxes empty).
  // Fill and submit can never be separated safely -- fuse them into one
  // retry unit so every submit attempt re-fills first.
  for (let i = 0; i < 6; i++) {
    // Run 28682845959: a successful submit can move the page past sign-in
    // -- straight to consent, or straight past consent too if this
    // user+client already has a grant -- before the Approve-visible wait
    // below resolves. An unguarded refill on the next attempt then fills a
    // detached email box and hangs until the test timeout.
    if (!(await emailBox.isVisible().catch(() => false))) break;
    await emailBox.fill(email, { timeout: 2_000 });
    await passwordBox.fill(password, { timeout: 2_000 });
    if (
      (await emailBox.inputValue()) !== email ||
      (await passwordBox.inputValue()) !== password
    ) {
      continue;
    }
    try {
      await page
        .getByRole("button", { name: /continue/i })
        .click({ timeout: 2_000 });
    } catch {
      // button may already be gone if a prior click's navigation landed late
    }
    try {
      await expect(approveButton).toBeVisible({ timeout: 5_000 });
      break;
    } catch {
      // retry
    }
  }

  // Run 28682845959: trace shows password grant 200, consent 200, straight
  // to the OWUI callback with no Approve click -- Supabase auto-approves a
  // previously-granted client+user pair, so the consent screen appears at
  // most once per user+client (first-ever run). Poll for either outcome
  // instead of asserting Approve will always show.
  const approvePollDeadline = Date.now() + 30_000;
  while (
    new URL(page.url()).origin !== owuiOrigin &&
    Date.now() < approvePollDeadline
  ) {
    if (await approveButton.isVisible().catch(() => false)) {
      // Lands back on /oauth/consent, now authenticated, showing the Hive
      // Chat client's requested scopes. Same hydration-race guard as
      // above: check first, click inside a try so a stale retry never
      // re-clicks a button that already navigated away, then wait in
      // short windows.
      for (let i = 0; i < 5; i++) {
        if (new URL(page.url()).origin === owuiOrigin) break;
        try {
          await page
            .getByRole("button", { name: /approve/i })
            .click({ timeout: 2_000 });
        } catch {
          // button may already be gone if a prior click's navigation landed late
        }
        try {
          await page.waitForURL((u) => u.origin === owuiOrigin, {
            timeout: 5_000,
          });
          break;
        } catch {
          // retry
        }
      }
      break;
    }
    await page.waitForTimeout(500);
  }

  // Run 28676421973: OAuth exchange itself verified fast and correct in
  // local repro, but OWUI's post-login SPA load (model-list fetch) can
  // outlast a short wait. Accept any OWUI-origin URL first, then give the
  // chat UI real time to finish loading.
  await page.waitForURL((u) => u.origin === owuiOrigin, {
    timeout: 30_000,
  });
}
