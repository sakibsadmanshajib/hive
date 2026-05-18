import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("first-token latency under budget", async ({ page }) => {
  await page.goto("/");
  await page.getByPlaceholder(/message/i).fill("one word.");
  const start = Date.now();
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes("/v1/chat/completions") && r.ok(),
    ),
    page.keyboard.press("Enter"),
  ]);
  const ttfb = Date.now() - start;
  // eslint-disable-next-line no-console
  console.log(`ttfb_ms=${ttfb}`);
  expect(ttfb).toBeLessThan(
    Number(process.env.OWUI_TTFB_BUDGET_MS ?? 8000),
  );
});
