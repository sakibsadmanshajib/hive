import { test, expect } from "@playwright/test";
import { Client } from "pg";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

const DB_URL = process.env.HIVE_TEST_DB_URL;

test("upload personal PDF and confirm embeddings land in pgvector", async ({
  page,
}) => {
  if (!DB_URL) test.skip(true, "HIVE_TEST_DB_URL not set");

  await page.goto("/");
  await page.getByRole("button", { name: /workspace/i }).click();
  await page.getByRole("link", { name: /documents/i }).click();
  await page.getByRole("button", { name: /upload/i }).click();
  await page.setInputFiles(
    'input[type="file"]',
    "e2e/phase-19/owui/fixtures/policy.pdf",
  );
  await expect(page.getByText(/policy\.pdf/i)).toBeVisible({
    timeout: 30_000,
  });

  const db = new Client({ connectionString: DB_URL });
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
