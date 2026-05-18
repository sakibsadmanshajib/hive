import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("second turn references first turn context", async ({ page }) => {
  await page.goto("/");
  await page.getByPlaceholder(/message/i).fill("My favourite colour is purple.");
  await page.keyboard.press("Enter");
  await page.waitForResponse(
    (r) => r.url().includes("/v1/chat/completions") && r.ok(),
    { timeout: 20_000 },
  );

  await page.getByPlaceholder(/message/i).fill("What is my favourite colour?");
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-role="assistant"]').last()).toContainText(
    /purple/i,
    { timeout: 20_000 },
  );
});
