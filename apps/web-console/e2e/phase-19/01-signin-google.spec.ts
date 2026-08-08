import { test, expect } from "@playwright/test";

import { requireEnv } from "./support/require-env";

test.use({ storageState: "e2e/phase-19/.auth/user-a.json" });

const OWUI_URL = process.env.OWUI_URL ?? "http://localhost:3003";

test("OAuth signup emits AUTH_SIGNUP_SUCCESS + TENANT_USER_ADD + OWUI_GROUP_ADD_SUCCESS", async ({
  page,
}) => {
  const DB_URL = requireEnv("HIVE_TEST_DB_URL");

  // Anchor the audit-log scan to a timestamp taken right before the UI
  // action so unrelated CI activity in the same partition can't satisfy
  // the assertion. The wider `now() - interval '5 minutes'` form was
  // prone to false positives on shared test DBs.
  const startedAt = new Date();
  await page.goto(OWUI_URL);
  await expect(page.getByText(/hive chat/i)).toBeVisible({ timeout: 15_000 });

  // pg is loaded dynamically so the spec compiles without a workspace
  // dependency; the CI lane that sets HIVE_TEST_DB_URL is responsible
  // for installing pg out-of-band before invoking the playwright runner.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const pgMod: any = await import("pg").catch(() => null);
  // Not a skip: the lane that sets HIVE_TEST_DB_URL is responsible for
  // installing pg, so a missing module means that install broke and this
  // spec would otherwise pass while asserting nothing.
  expect(pgMod, "pg module not installed").not.toBeNull();

  const db = new pgMod.Client({ connectionString: DB_URL });
  await db.connect();
  try {
    const res = await db.query(
      `SELECT action
         FROM public.audit_log
        WHERE ts >= $1
          AND action = ANY($2::text[])`,
      [startedAt, ["AUTH_SIGNUP_SUCCESS", "TENANT_USER_ADD", "OWUI_GROUP_ADD_SUCCESS"]],
    );
    const actions = res.rows.map((r: { action: string }) => r.action);
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
