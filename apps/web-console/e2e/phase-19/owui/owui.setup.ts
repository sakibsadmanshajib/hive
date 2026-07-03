import { test as setup, expect } from "@playwright/test";

const STATE = "e2e/phase-19/owui/.auth/owui-user.json";
const OWUI_URL = process.env.OWUI_URL ?? "http://localhost:3002";

setup("OWUI OIDC sign-in via Hive consent", async ({ page }) => {
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
  // Without this wait, the fills below race the redirect chain and land on
  // OWUI's own native login form instead of the web-console one.
  await page.waitForURL(/\/(auth\/sign-in|oauth\/consent)/, {
    timeout: 30_000,
  });

  // getByLabel("Password") is a strict-mode violation here: the browser's
  // native password-reveal toggle button shares "Password" in its
  // accessible name. getByRole("textbox", ...) excludes it by role.
  const emailBox = page.getByRole("textbox", { name: /email/i });
  const passwordBox = page.getByRole("textbox", { name: /password/i });
  // web-console runs in dev mode in CI; a fill that lands before React
  // hydration finishes gets wiped when the controlled input mounts. Retry
  // both fills together and verify the values actually stuck.
  for (let i = 0; i < 3; i++) {
    await emailBox.fill(email);
    await passwordBox.fill(password);
    if (
      (await emailBox.inputValue()) === email &&
      (await passwordBox.inputValue()) === password
    ) {
      break;
    }
  }
  await expect(emailBox).toHaveValue(email);
  await expect(passwordBox).toHaveValue(password);

  // Same hydration race as the fills: a click that lands before React
  // attaches the handler is silently lost (run 28676903599: zero
  // /oauth/oidc/callback hits after this step, both attempts). Retry the
  // click until the navigation actually happens. A successful click can
  // still outlast the 5s wait below -- check the URL first and guard the
  // click itself so a stale retry never re-clicks a button that already
  // navigated away.
  for (let i = 0; i < 5; i++) {
    if (/\/oauth\/consent/.test(page.url())) break;
    try {
      await page
        .getByRole("button", { name: /continue/i })
        .click({ timeout: 2_000 });
    } catch {
      // button may already be gone if a prior click's navigation landed late
    }
    try {
      await page.waitForURL(/\/oauth\/consent/, { timeout: 5_000 });
      break;
    } catch {
      // retry
    }
  }
  expect(page.url()).toMatch(/\/oauth\/consent/);

  // Lands back on /oauth/consent, now authenticated, showing the Hive Chat
  // client's requested scopes. Same hydration-race guard as above: check
  // first, click inside a try so a stale retry never re-clicks a button
  // that already navigated away, then wait in short windows.
  const owuiOrigin = new URL(OWUI_URL).origin;
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
