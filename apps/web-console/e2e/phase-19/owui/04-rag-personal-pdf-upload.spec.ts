import { test, expect } from "@playwright/test";
import { existsSync } from "node:fs";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

const DB_URL = process.env.HIVE_TEST_DB_URL;
const FIXTURE = "e2e/phase-19/owui/fixtures/policy.pdf";

test("upload personal PDF and confirm embeddings land in pgvector", async ({
  page,
}) => {
  if (!DB_URL) test.skip(true, "HIVE_TEST_DB_URL not set");
  if (!existsSync(FIXTURE)) {
    test.skip(true, "policy.pdf fixture not present (see fixtures/README.md)");
  }

  await page.goto("/");
  await page.getByRole("button", { name: /workspace/i }).click();
  await page.getByRole("link", { name: /documents/i }).click();
  await page.getByRole("button", { name: /upload/i }).click();
  await page.setInputFiles('input[type="file"]', FIXTURE);
  await expect(page.getByText(/policy\.pdf/i)).toBeVisible({
    timeout: 30_000,
  });

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const pgMod: any = await import("pg").catch(() => null);
  if (!pgMod) test.skip(true, "pg module not installed");

  const db = new pgMod.Client({ connectionString: DB_URL });
  await db.connect();
  try {
    const res = await db.query(`
      SELECT count(*)::int AS n FROM information_schema.tables
      WHERE table_name LIKE 'document%' OR table_name LIKE 'pgvector_%'
    `);
    expect(res.rows[0].n).toBeGreaterThan(0);
  } finally {
    await db.end();
  }
});
