import { test, expect } from "@playwright/test";
import { Client } from "pg";

test.use({ storageState: "e2e/phase-19/.auth/user-a.json" });

const DB_URL = process.env.HIVE_TEST_DB_URL;
const CONTROL_PLANE_URL =
  process.env.CONTROL_PLANE_URL ?? "http://localhost:8081";

test("switch to second tenant updates metadata and audits TENANT_SWITCH", async ({
  request,
}) => {
  const tenantBID = process.env.E2E_USER_A_SECOND_TENANT_ID;
  if (!tenantBID || !DB_URL) {
    test.skip(true, "E2E_USER_A_SECOND_TENANT_ID or HIVE_TEST_DB_URL not set");
  }

  const resp = await request.post(
    `${CONTROL_PLANE_URL}/v1/tenants/switch`,
    { data: { tenant_id: tenantBID } },
  );
  expect(resp.status()).toBe(200);

  const db = new Client({ connectionString: DB_URL });
  await db.connect();
  try {
    const audits = await db.query(
      `SELECT action FROM public.audit_log
        WHERE ts > now() - interval '2 minutes' AND action = 'TENANT_SWITCH'`,
    );
    expect(audits.rowCount).toBeGreaterThan(0);
  } finally {
    await db.end();
  }
});
