import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("user sees only models granted to tenant group", async ({ page }) => {
  await page.goto("/");
  await page.getByTestId("model-selector").click();
  const options = await page.getByRole("option").all();
  // Guard against an empty option list (e.g. catalog not seeded) — that
  // would vacuously pass the loop below and hide a real visibility bug.
  expect(options.length).toBeGreaterThan(0);
  for (const opt of options) {
    const text = (await opt.textContent()) ?? "";
    // grok is intentionally NOT granted to the test tenant group
    expect(text).not.toMatch(/grok/i);
  }
});
