import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("chat message streams a response", async ({ page }) => {
  await page.goto("/");
  await page.getByPlaceholder(/message/i).fill("Say hello.");
  await page.keyboard.press("Enter");
  const reply = page.locator('[data-role="assistant"]').last();
  await expect(reply).toContainText(/.+/, { timeout: 20_000 });
});
