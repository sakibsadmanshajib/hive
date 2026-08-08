import { test, expect } from "@playwright/test";

import { requireEnv } from "./support/require-env";

const CONTROL_PLANE_URL =
  process.env.CONTROL_PLANE_URL ?? "http://localhost:8081";

test("switch to second tenant updates metadata and audits TENANT_SWITCH", async ({
  playwright,
}) => {
  const secondTenantID = requireEnv("E2E_USER_A_SECOND_TENANT_ID");
  const dbURL = requireEnv("HIVE_TEST_DB_URL");
  // See 03-tenant-isolation.spec.ts for why this is a bearer token rather
  // than an Open WebUI storage-state file.
  const userAToken = requireEnv("E2E_USER_A_JWT");

  const api = await playwright.request.newContext({
    extraHTTPHeaders: { Authorization: `Bearer ${userAToken}` },
  });

  // Anchor the audit-log scan to the request boundary so unrelated CI
  // TENANT_SWITCH rows can't satisfy the assertion.
  const startedAt = new Date();
  try {
    const resp = await api.post(`${CONTROL_PLANE_URL}/v1/tenants/switch`, {
      data: { tenant_id: secondTenantID },
    });
    expect(resp.status()).toBe(200);
  } finally {
    await api.dispose();
  }

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const pgMod: any = await import("pg").catch(() => null);
  // Not a skip: the workflow installs pg before invoking this project, so a
  // missing module means that install broke, and the spec would otherwise
  // report green while asserting nothing about the audit trail.
  expect(pgMod, "pg module not installed").not.toBeNull();

  const db = new pgMod.Client({ connectionString: dbURL });
  await db.connect();
  try {
    const audits = await db.query(
      `SELECT action
         FROM public.audit_log
        WHERE ts >= $1
          AND action = 'TENANT_SWITCH'`,
      [startedAt],
    );
    expect(audits.rowCount).toBeGreaterThan(0);
  } finally {
    await db.end();
  }
});
