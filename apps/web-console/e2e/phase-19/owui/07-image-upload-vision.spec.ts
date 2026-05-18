import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("upload image and vision model returns content-aware answer", async ({
  page,
}) => {
  await page.goto("/");
  await page.setInputFiles(
    'input[type="file"]',
    "e2e/phase-19/owui/fixtures/cat.jpg",
  );
  await page.getByPlaceholder(/message/i).fill("What animal is in this image?");
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-role="assistant"]').last()).toContainText(
    /cat/i,
    { timeout: 30_000 },
  );
});
