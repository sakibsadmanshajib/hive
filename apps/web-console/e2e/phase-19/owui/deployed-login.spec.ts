import { test, expect } from "@playwright/test";

// Deployed-origin OIDC sign-in smoke. owui.setup.ts already covers this
// journey, but it also installs the hive_jwt_forward Function and writes
// storageState, and both of those mutate whatever they point at. Neither is
// acceptable against a live deployment, so this spec logs in and asserts,
// nothing else.
//
// It exists because the consent page is served from Supabase's site_url,
// which is a different origin from the chat surface (chat-hive.scubed.co ->
// console-hive.scubed.co -> back). The nightly exercises that hop with the
// chat surface on localhost; this one exercises it with both sides deployed,
// which is the only configuration a real user ever sees.
const OWUI_URL = process.env.OWUI_URL ?? "";

test("OIDC sign-in completes against the deployed origin", async ({ page }) => {
  const isDeployed =
    new URL(OWUI_URL || "http://localhost").hostname !== "localhost";
  if (!isDeployed) {
    test.skip(true, "OWUI_URL is not a deployed origin");
    return;
  }

  const email = process.env.OWUI_E2E_EMAIL;
  const password = process.env.OWUI_E2E_PASSWORD;
  if (!email || !password) {
    test.skip(true, "OWUI_E2E_EMAIL/OWUI_E2E_PASSWORD not set");
    return;
  }

  const owuiOrigin = new URL(OWUI_URL).origin;
  await page.goto("/auth");

  const hiveButton = page.getByRole("button", { name: /continue with hive/i });
  await expect(hiveButton).toBeVisible({ timeout: 30_000 });
  // The login page animates continuously, so Playwright's click-stability
  // check never settles and force-click still requires the element in the
  // viewport. dispatchEvent fires the handler regardless of geometry.
  await hiveButton.dispatchEvent("click");

  const emailBox = page.getByRole("textbox", { name: /email/i });
  const passwordBox = page.getByRole("textbox", { name: /password/i });
  const approveButton = page.getByRole("button", { name: /approve/i });
  const newChatButton = page.getByRole("button", { name: /new chat/i });

  // Consent renders first and only then bounces to sign-in once its
  // client-side session check comes back empty, so a URL check can observe
  // the transient consent pathname and wrongly conclude we are past login.
  // Wait on the DOM instead.
  await expect(emailBox.or(approveButton)).toBeVisible({ timeout: 45_000 });

  if (await emailBox.isVisible()) {
    await emailBox.fill(email);
    await passwordBox.fill(password);
    await page.getByRole("button", { name: /continue/i }).click();
  }

  // Supabase auto-approves a client the user has already granted, so consent
  // shows at most once per user and client. Both outcomes are valid.
  await expect(approveButton.or(newChatButton)).toBeVisible({ timeout: 45_000 });
  if (await approveButton.isVisible()) {
    await approveButton.click();
  }

  await page.waitForURL((u) => u.origin === owuiOrigin, { timeout: 45_000 });
  await expect(newChatButton).toBeVisible({ timeout: 60_000 });
});
