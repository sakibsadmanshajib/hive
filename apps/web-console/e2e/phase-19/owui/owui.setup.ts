import { test as setup, expect } from "@playwright/test";

const STATE = "e2e/phase-19/owui/.auth/owui-user.json";

setup("OWUI OIDC sign-in via Hive consent", async ({ page }) => {
  const email = process.env.OWUI_E2E_EMAIL;
  const password = process.env.OWUI_E2E_PASSWORD;
  if (!email || !password) {
    setup.skip(true, "OWUI_E2E_* env not set");
    return;
  }

  await page.goto("/");
  await page.getByRole("button", { name: /sign in with hive/i }).click();

  // The OAuth click starts a real full-page redirect chain: OWUI -> Supabase
  // authorize -> /oauth/consent (web-console origin, unauthenticated) ->
  // /auth/sign-in?next=... (still web-console origin). The consent and
  // sign-in pages live on the web-console's Supabase Site URL, a different
  // origin from OWUI_URL, so this spec never calls page.goto with a relative
  // path past this point -- it only follows whatever the browser is
  // redirected to, which keeps it baseURL-agnostic.
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: /continue/i }).click();

  // Lands back on /oauth/consent, now authenticated, showing the Hive Chat
  // client's requested scopes.
  await page.getByRole("button", { name: /approve/i }).click();

  // approveAuthorization()'s redirect_url sends the browser back to OWUI's
  // OIDC callback, completing sign-in.
  await expect(page.getByPlaceholder(/message/i)).toBeVisible({
    timeout: 30_000,
  });
  await page.context().storageState({ path: STATE });
});
