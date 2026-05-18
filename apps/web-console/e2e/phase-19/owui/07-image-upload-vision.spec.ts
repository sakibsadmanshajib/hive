import { test, expect } from "@playwright/test";
import { existsSync } from "node:fs";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

const FIXTURE = "e2e/phase-19/owui/fixtures/cat.jpg";

test("upload image and vision model returns content-aware answer", async ({
  page,
}) => {
  if (!existsSync(FIXTURE)) {
    test.skip(true, "cat.jpg fixture not present (see fixtures/README.md)");
  }

  await page.goto("/");

  // Pick a vision-capable model before submitting so the assertion can't
  // be satisfied by a text-only model that hallucinated "cat".
  await page.getByTestId("model-selector").click();
  await page.getByRole("option", { name: /vision|gpt-4o|claude-3\.5|gemini/i }).first().click();

  await page.setInputFiles('input[type="file"]', FIXTURE);

  // The uploaded image must surface as an attachment thumbnail before
  // the prompt fires — confirms the vision path is exercised, not a
  // text completion that happens to mention "cat".
  await expect(
    page.locator('[data-attachment-name*="cat"], img[alt*="cat" i], [data-role="attachment"]').first(),
  ).toBeVisible({ timeout: 15_000 });

  await page.getByPlaceholder(/message/i).fill("What animal is in this image?");
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-role="assistant"]').last()).toContainText(
    /cat/i,
    { timeout: 30_000 },
  );
});
