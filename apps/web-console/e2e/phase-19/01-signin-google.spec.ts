import { test, expect } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/.auth/user-a.json" });

const DB_URL = process.env.HIVE_TEST_DB_URL;
const OWUI_URL = process.env.OWUI_URL ?? "http://localhost:3003";

test("OAuth signup emits AUTH_SIGNUP_SUCCESS + TENANT_USER_ADD + OWUI_GROUP_ADD_SUCCESS", async ({
  page,
}) => {
  if (!DB_URL) test.skip(true, "HIVE_TEST_DB_URL not set");

  await page.goto(OWUI_URL);
  await expect(page.getByText(/hive chat/i)).toBeVisible({ timeout: 15_000 });

  // pg is loaded dynamically so the spec compiles without a workspace
  // dependency; the CI lane that sets HIVE_TEST_DB_URL is responsible
  // for installing pg out-of-band before invoking the playwright runner.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const pgMod: any = await import("pg").catch(() => null);
  if (!pgMod) test.skip(true, "pg module not installed");

  const db = new pgMod.Client({ connectionString: DB_URL });
  await db.connect();
  try {
    const res = await db.query(`
      SELECT action FROM public.audit_log
       WHERE ts > now() - interval '5 minutes'
         AND action IN ('AUTH_SIGNUP_SUCCESS','TENANT_USER_ADD','OWUI_GROUP_ADD_SUCCESS')
    `);
    const actions = res.rows.map((r) => r.action);
    expect(actions).toEqual(
      expect.arrayContaining([
        "AUTH_SIGNUP_SUCCESS",
        "TENANT_USER_ADD",
        "OWUI_GROUP_ADD_SUCCESS",
      ]),
    );
  } finally {
    await db.end();
  }
});
