import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("ask grounded question and receive citation", async ({ page }) => {
  const expected = JSON.parse(
    readFileSync("e2e/phase-19/owui/fixtures/expected-citations.json", "utf8"),
  ) as { prompt: string; anchor: string };

  await page.goto("/");
  await page.getByPlaceholder(/message/i).fill(expected.prompt);
  await page.keyboard.press("Enter");
  const reply = page.locator('[data-role="assistant"]').last();
  await expect(reply).toContainText(new RegExp(expected.anchor, "i"), {
    timeout: 30_000,
  });
  await expect(page.getByText(/policy\.pdf/i)).toBeVisible();
});
