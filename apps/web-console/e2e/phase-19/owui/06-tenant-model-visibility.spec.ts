import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("user sees only models granted to tenant group", async ({ page }) => {
  await page.goto("/");
  await page.getByTestId("model-selector").click();
  const options = await page.getByRole("option").all();
  for (const opt of options) {
    const text = (await opt.textContent()) ?? "";
    // grok is intentionally NOT granted to the test tenant group
    expect(text).not.toMatch(/grok/i);
  }
});
