import { test, expect } from "@playwright/test";

import { requireEnv } from "./support/require-env";

test.use({ storageState: "e2e/phase-19/.auth/user-a.json" });

const OWUI_URL = process.env.OWUI_URL ?? "http://localhost:3003";

test("first chat streams response and writes llm_traces + CHAT_REQUEST", async ({
  page,
}) => {
  const DB_URL = requireEnv("HIVE_TEST_DB_URL");

  await page.goto(OWUI_URL);
  await page.getByPlaceholder(/message/i).fill("hi");
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-role="assistant"]').last()).toContainText(
    /.+/,
    { timeout: 30_000 },
  );

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const pgMod: any = await import("pg").catch(() => null);
  // Not a skip: see 01-signin-google.spec.ts.
  expect(pgMod, "pg module not installed").not.toBeNull();

  const db = new pgMod.Client({ connectionString: DB_URL });
  await db.connect();
  try {
    const traces = await db.query(
      `SELECT count(*)::int AS n FROM public.llm_traces WHERE ts > now() - interval '2 minutes'`,
    );
    expect(traces.rows[0].n).toBeGreaterThan(0);

    const actions = await db.query(
      `SELECT action FROM public.audit_log
        WHERE ts > now() - interval '2 minutes' AND action = 'CHAT_REQUEST'`,
    );
    expect(actions.rowCount).toBeGreaterThan(0);
  } finally {
    await db.end();
  }
});
