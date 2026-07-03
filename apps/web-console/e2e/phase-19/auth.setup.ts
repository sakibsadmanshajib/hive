import { test as setup, expect } from "@playwright/test";

const OWUI_URL = process.env.OWUI_URL ?? "http://localhost:3003";
const STATE_A = "e2e/phase-19/.auth/user-a.json";
const STATE_B = "e2e/phase-19/.auth/user-b.json";

setup("authenticate user A (tenant T1)", async ({ page }) => {
  const email = process.env.E2E_USER_A_EMAIL;
  const password = process.env.E2E_USER_A_PASSWORD;
  if (!email || !password) {
    setup.skip(true, "E2E_USER_A_* env not set");
    return;
  }
  await page.goto(`${OWUI_URL}/`);
  // ponytail: OWUI login page has a continuously animating element, so
  // Playwright's click-stability check never settles, and its force-click still
  // requires the element in the viewport, which fails during the same
  // animation. dispatchEvent fires the DOM click handler directly, regardless of
  // geometry, stability, or overlays.
  const hiveButton = page.getByRole("button", { name: /continue with hive/i });
  await expect(hiveButton).toBeVisible({ timeout: 30_000 });
  await hiveButton.dispatchEvent("click");
  // Without this wait, the fills below race the redirect chain and land on
  // OWUI's own native login form instead of the web-console one.
  await page.waitForURL(/\/(auth\/sign-in|oauth\/consent)/, {
    timeout: 30_000,
  });

  // getByLabel("Password") is a strict-mode violation here: the browser's
  // native password-reveal toggle button shares "Password" in its
  // accessible name. getByRole("textbox", ...) excludes it by role.
  await page.getByRole("textbox", { name: /email/i }).fill(email);
  await page.getByRole("button", { name: /next/i }).click();
  await page.getByRole("textbox", { name: /password/i }).fill(password);
  await page.getByRole("button", { name: /sign in/i }).click();
  await expect(page).toHaveURL(/localhost:3003/, { timeout: 30_000 });
  await page.context().storageState({ path: STATE_A });
});

setup("authenticate user B (tenant T2)", async ({ page }) => {
  const email = process.env.E2E_USER_B_EMAIL;
  const password = process.env.E2E_USER_B_PASSWORD;
  if (!email || !password) {
    setup.skip(true, "E2E_USER_B_* env not set");
    return;
  }
  await page.goto(`${OWUI_URL}/`);
  // ponytail: OWUI login page has a continuously animating element, so
  // Playwright's click-stability check never settles, and its force-click still
  // requires the element in the viewport, which fails during the same
  // animation. dispatchEvent fires the DOM click handler directly, regardless of
  // geometry, stability, or overlays.
  const hiveButton = page.getByRole("button", { name: /continue with hive/i });
  await expect(hiveButton).toBeVisible({ timeout: 30_000 });
  await hiveButton.dispatchEvent("click");
  // Without this wait, the fills below race the redirect chain and land on
  // OWUI's own native login form instead of the web-console one.
  await page.waitForURL(/\/(auth\/sign-in|oauth\/consent)/, {
    timeout: 30_000,
  });

  // getByLabel("Password") is a strict-mode violation here: the browser's
  // native password-reveal toggle button shares "Password" in its
  // accessible name. getByRole("textbox", ...) excludes it by role.
  await page.getByRole("textbox", { name: /email/i }).fill(email);
  await page.getByRole("button", { name: /next/i }).click();
  await page.getByRole("textbox", { name: /password/i }).fill(password);
  await page.getByRole("button", { name: /sign in/i }).click();
  await expect(page).toHaveURL(/localhost:3003/, { timeout: 30_000 });
  await page.context().storageState({ path: STATE_B });
});
