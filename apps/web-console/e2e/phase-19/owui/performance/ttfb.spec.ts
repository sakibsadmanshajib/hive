import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

function budgetMs(envName: string, fallback: number): number {
  const raw = process.env[envName];
  if (raw === undefined || raw === "") return fallback;
  const parsed = Number(raw);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    throw new Error(`${envName} must be a positive finite number, got "${raw}"`);
  }
  return parsed;
}

test("first-token latency under budget", async ({ page }) => {
  const budget = budgetMs("OWUI_TTFB_BUDGET_MS", 8000);
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
  expect(ttfb).toBeLessThan(budget);
});
