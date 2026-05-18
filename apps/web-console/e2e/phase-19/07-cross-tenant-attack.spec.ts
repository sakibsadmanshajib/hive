import { test, expect } from "@playwright/test";
import { Client } from "pg";

test.use({ storageState: "e2e/phase-19/.auth/user-a.json" });

const DB_URL = process.env.HIVE_TEST_DB_URL;
const CONTROL_PLANE_URL =
  process.env.CONTROL_PLANE_URL ?? "http://localhost:8081";

test("crafting a body with another tenant_id is denied and audited CRITICAL", async ({
  request,
}) => {
  const tenantBID = process.env.E2E_TENANT_B_ID;
  if (!tenantBID || !DB_URL) {
    test.skip(true, "E2E_TENANT_B_ID or HIVE_TEST_DB_URL not set");
  }

  // User A is NOT a member of tenant B.
  const resp = await request.post(
    `${CONTROL_PLANE_URL}/v1/tenants/switch`,
    { data: { tenant_id: tenantBID } },
  );
  expect(resp.status()).toBe(403);
  const body = await resp.json();
  expect(body.error?.code).toBe("CROSS_TENANT");

  const db = new Client({ connectionString: DB_URL });
  await db.connect();
  try {
    const audits = await db.query(`
      SELECT action, severity FROM public.audit_log
       WHERE ts > now() - interval '2 minutes' AND action = 'CROSS_TENANT_ATTEMPT'
    `);
    expect(audits.rowCount).toBeGreaterThan(0);
    expect(audits.rows[0].severity).toBe("CRITICAL");
  } finally {
    await db.end();
  }
});
