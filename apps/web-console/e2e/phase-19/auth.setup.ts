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
  // OWUI's own native login form instead of the web-console one. The
  // sign-in URL carries the consent path inside its `next` query param
  // (run 28681138594), so regex/substring matching on the full URL
  // false-positives on the sign-in page too -- match pathname only.
  await page.waitForURL(
    (u) => u.pathname === "/auth/sign-in" || u.pathname === "/oauth/consent",
    { timeout: 30_000 },
  );

  // getByLabel("Password") is a strict-mode violation here: the browser's
  // native password-reveal toggle button shares "Password" in its
  // accessible name. getByRole("textbox", ...) excludes it by role.
  const emailBox = page.getByRole("textbox", { name: /email/i });
  const passwordBox = page.getByRole("textbox", { name: /password/i });
  // web-console runs in dev mode in CI; React hydration can remount the
  // controlled input *after* a fill already verified as stuck, wiping it, so
  // a submit fired after that remount hits an empty field (run 28680373668:
  // "missing email or phone" alert, both textboxes empty). Fill and submit
  // can never be separated safely -- fuse them into one retry unit so every
  // submit attempt re-fills first.
  for (let i = 0; i < 6; i++) {
    if (await passwordBox.isVisible().catch(() => false)) break;
    await emailBox.fill(email);
    if ((await emailBox.inputValue()) !== email) continue;
    try {
      await page
        .getByRole("button", { name: /next/i })
        .click({ timeout: 2_000 });
    } catch {
      // button may already be gone if a prior click's navigation landed late
    }
    try {
      await expect(passwordBox).toBeVisible({ timeout: 5_000 });
      break;
    } catch {
      // retry
    }
  }
  await expect(passwordBox).toBeVisible();

  // Same fusion, same reason: the password field can be wiped after a
  // verified fill, so refill it on every sign-in attempt too (run
  // 28680373668: "missing email or phone" alert, both textboxes empty).
  const owuiOrigin = new URL(OWUI_URL).origin;
  for (let i = 0; i < 6; i++) {
    if (new URL(page.url()).origin === owuiOrigin) break;
    await passwordBox.fill(password);
    if ((await passwordBox.inputValue()) !== password) continue;
    try {
      await page
        .getByRole("button", { name: /sign in/i })
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
  expect(new URL(page.url()).origin).toBe(owuiOrigin);
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
  // OWUI's own native login form instead of the web-console one. The
  // sign-in URL carries the consent path inside its `next` query param
  // (run 28681138594), so regex/substring matching on the full URL
  // false-positives on the sign-in page too -- match pathname only.
  await page.waitForURL(
    (u) => u.pathname === "/auth/sign-in" || u.pathname === "/oauth/consent",
    { timeout: 30_000 },
  );

  // getByLabel("Password") is a strict-mode violation here: the browser's
  // native password-reveal toggle button shares "Password" in its
  // accessible name. getByRole("textbox", ...) excludes it by role.
  const emailBox = page.getByRole("textbox", { name: /email/i });
  const passwordBox = page.getByRole("textbox", { name: /password/i });
  // web-console runs in dev mode in CI; React hydration can remount the
  // controlled input *after* a fill already verified as stuck, wiping it, so
  // a submit fired after that remount hits an empty field (run 28680373668:
  // "missing email or phone" alert, both textboxes empty). Fill and submit
  // can never be separated safely -- fuse them into one retry unit so every
  // submit attempt re-fills first.
  for (let i = 0; i < 6; i++) {
    if (await passwordBox.isVisible().catch(() => false)) break;
    await emailBox.fill(email);
    if ((await emailBox.inputValue()) !== email) continue;
    try {
      await page
        .getByRole("button", { name: /next/i })
        .click({ timeout: 2_000 });
    } catch {
      // button may already be gone if a prior click's navigation landed late
    }
    try {
      await expect(passwordBox).toBeVisible({ timeout: 5_000 });
      break;
    } catch {
      // retry
    }
  }
  await expect(passwordBox).toBeVisible();

  // Same fusion, same reason: the password field can be wiped after a
  // verified fill, so refill it on every sign-in attempt too (run
  // 28680373668: "missing email or phone" alert, both textboxes empty).
  const owuiOrigin = new URL(OWUI_URL).origin;
  for (let i = 0; i < 6; i++) {
    if (new URL(page.url()).origin === owuiOrigin) break;
    await passwordBox.fill(password);
    if ((await passwordBox.inputValue()) !== password) continue;
    try {
      await page
        .getByRole("button", { name: /sign in/i })
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
  expect(new URL(page.url()).origin).toBe(owuiOrigin);
  await page.context().storageState({ path: STATE_B });
});
