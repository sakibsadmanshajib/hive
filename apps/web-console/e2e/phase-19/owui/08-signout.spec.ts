import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("signout clears session and lands on signin page", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: /account/i }).click();
  await page.getByRole("menuitem", { name: /sign out/i }).click();
  await expect(
    page.getByRole("button", { name: /continue with hive/i }),
  ).toBeVisible();
});
