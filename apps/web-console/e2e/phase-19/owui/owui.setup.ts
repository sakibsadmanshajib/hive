import { test as setup, expect } from "@playwright/test";

const STATE = "e2e/phase-19/owui/.auth/owui-user.json";

setup("OWUI OIDC sign-in", async ({ page }) => {
  const email = process.env.OWUI_E2E_EMAIL;
  const password = process.env.OWUI_E2E_PASSWORD;
  if (!email || !password) {
    setup.skip(true, "OWUI_E2E_* env not set");
    return;
  }

  await page.goto("/");
  await page.getByRole("button", { name: /sign in with hive/i }).click();
  await page.getByLabel("Email").fill(email);
  await page.getByRole("button", { name: /next/i }).click();
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: /sign in/i }).click();
  await expect(page.getByPlaceholder(/message/i)).toBeVisible({
    timeout: 30_000,
  });
  await page.context().storageState({ path: STATE });
});
