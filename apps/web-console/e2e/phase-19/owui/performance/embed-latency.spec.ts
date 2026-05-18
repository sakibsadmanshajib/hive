import { test, expect } from "@playwright/test";
import { existsSync } from "node:fs";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

const FIXTURE = "e2e/phase-19/owui/fixtures/policy.pdf";

function budgetMs(envName: string, fallback: number): number {
  const raw = process.env[envName];
  if (raw === undefined || raw === "") return fallback;
  const parsed = Number(raw);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    throw new Error(`${envName} must be a positive finite number, got "${raw}"`);
  }
  return parsed;
}

test("personal-doc ingestion under budget", async ({ page }) => {
  if (!existsSync(FIXTURE)) {
    test.skip(true, "policy.pdf fixture not present (see fixtures/README.md)");
  }
  const budget = budgetMs("OWUI_EMBED_BUDGET_MS", 30_000);

  await page.goto("/");
  await page.getByRole("button", { name: /workspace/i }).click();
  await page.getByRole("link", { name: /documents/i }).click();
  const before = Date.now();
  await page.getByRole("button", { name: /upload/i }).click();
  await page.setInputFiles('input[type="file"]', FIXTURE);
  // Scope the readiness check to the row that carries the uploaded
  // filename so we don't latch onto a pre-existing document's state.
  const row = page.getByRole("row", { name: /policy\.pdf/i }).first();
  await expect(row.getByText(/ready|indexed/i)).toBeVisible({
    timeout: 60_000,
  });
  const ms = Date.now() - before;
  // eslint-disable-next-line no-console
  console.log(`embed_ms=${ms}`);
  expect(ms).toBeLessThan(budget);
});
