import { test as setup, expect } from "@playwright/test";

const STATE = "e2e/phase-19/owui/.auth/owui-user.json";
const OWUI_URL = process.env.OWUI_URL ?? "http://localhost:3002";

setup("OWUI OIDC sign-in via Hive consent", async ({ page }) => {
  // Run 28681926134: consent can load first, then its client-side session
  // check bounces to sign-in -- give the retry-heavy journey below real
  // time to survive that bounce instead of the 30s test default.
  setup.setTimeout(180_000);

  const email = process.env.OWUI_E2E_EMAIL;
  const password = process.env.OWUI_E2E_PASSWORD;
  // SUPABASE_OAUTH_CLIENT_ID/SECRET are a separate, ops-provisioned pair
  // (Supabase OAuth App registration) -- without them the "Continue with
  // Hive" button on OWUI has no functional OAuth client behind it, so this
  // whole journey cannot run yet. Skip cleanly rather than hard-fail.
  const oauthClientId = process.env.SUPABASE_OAUTH_CLIENT_ID;
  const oauthClientSecret = process.env.SUPABASE_OAUTH_CLIENT_SECRET;
  if (!email || !password || !oauthClientId || !oauthClientSecret) {
    setup.skip(true, "OWUI_E2E_*/SUPABASE_OAUTH_CLIENT_* env not set");
    return;
  }

  await page.goto("/");
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
  // origin from OWUI_URL, so this spec never calls page.goto with a relative
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
  const owuiOrigin = new URL(OWUI_URL).origin;
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
  await expect(page.getByPlaceholder(/message/i)).toBeVisible({
    timeout: 60_000,
  });
  await page.context().storageState({ path: STATE });
});
