import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("signout clears session and lands on signin page", async ({ page }) => {
  await page.goto("/");
  // No accessible "account" button exists in OWUI 0.9.5. The profile menu
  // trigger is an <img alt="Open User Profile Menu"> in Sidebar.svelte,
  // visible in the header regardless of collapsed/expanded sidebar state
  // (verified against the run 28684729556 failure snapshot).
  await page.getByRole("img", { name: /open user profile menu/i }).click();
  await page.getByRole("menuitem", { name: /sign out/i }).click();
  await expect(
    page.getByRole("button", { name: /continue with hive/i }),
  ).toBeVisible();
});
