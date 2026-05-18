import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

test("personal-doc ingestion under budget", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: /workspace/i }).click();
  await page.getByRole("link", { name: /documents/i }).click();
  const before = Date.now();
  await page.getByRole("button", { name: /upload/i }).click();
  await page.setInputFiles(
    'input[type="file"]',
    "e2e/phase-19/owui/fixtures/policy.pdf",
  );
  await expect(page.getByText(/ready|indexed/i)).toBeVisible({
    timeout: 60_000,
  });
  const ms = Date.now() - before;
  // eslint-disable-next-line no-console
  console.log(`embed_ms=${ms}`);
  expect(ms).toBeLessThan(
    Number(process.env.OWUI_EMBED_BUDGET_MS ?? 30_000),
  );
});
