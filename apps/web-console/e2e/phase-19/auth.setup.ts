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
  await page.getByRole("button", { name: /sign in with hive/i }).click();
  await page.getByLabel("Email").fill(email);
  await page.getByRole("button", { name: /next/i }).click();
  await page.getByLabel("Password").fill(password);
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
  await page.getByRole("button", { name: /sign in with hive/i }).click();
  await page.getByLabel("Email").fill(email);
  await page.getByRole("button", { name: /next/i }).click();
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: /sign in/i }).click();
  await expect(page).toHaveURL(/localhost:3003/, { timeout: 30_000 });
  await page.context().storageState({ path: STATE_B });
});
